package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// EVIOCGRAB takes a device exclusively, so its keys do not also reach whatever
// has focus. _IOW('E', 0x90, int).
const eviocgrab = 0x40044590

// eventReader turns key-down events from several devices into one stream.
type eventReader struct {
	files      []*os.File
	grabbed    []*os.File
	events     chan uint16
	tty        *os.File
	ttyRestore func()
}

// openReader opens each device and starts reading it.
//
// Grabbing is right when reading the keypad directly. It is wrong when reading
// keyd's virtual device, because keyd may be routing other keyboards through
// it and grabbing would take those too; there the terminal is quietened
// instead, which keeps interrupt characters working.
func openReader(devices []inputDevice, grab bool) (*eventReader, error) {
	r := &eventReader{events: make(chan uint16, 256)}
	for _, device := range devices {
		file, err := os.Open(device.Path)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("cannot read %s: %w", device.Path, err)
		}
		r.files = append(r.files, file)
		if grab {
			if err := ioctlGrab(file, 1); err != nil {
				fmt.Fprintf(os.Stderr,
					"note: could not take %s exclusively (%v); its keys will also\n"+
						"      reach the focused window\n", device.Path, err)
			} else {
				r.grabbed = append(r.grabbed, file)
			}
		}
		go r.pump(file)
	}
	if !grab {
		r.quietenTerminal()
	}
	return r, nil
}

func ioctlGrab(file *os.File, on uintptr) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL, file.Fd(), uintptr(eviocgrab), on)
	if errno != 0 {
		return errno
	}
	return nil
}

// pump reads one device forever, forwarding key presses.
func (r *eventReader) pump(file *os.File) {
	buffer := make([]byte, eventSize*32)
	for {
		n, err := file.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+eventSize <= n; offset += eventSize {
			record := buffer[offset : offset+eventSize]
			kind := binary.LittleEndian.Uint16(record[16:18])
			code := binary.LittleEndian.Uint16(record[18:20])
			value := int32(binary.LittleEndian.Uint32(record[20:24]))
			if kind == evKey && value == 1 {
				select {
				case r.events <- code:
				default: // a full buffer means nobody is waiting; drop it
				}
			}
		}
	}
}

// Next waits for the next key press, reporting false if none arrives in time.
func (r *eventReader) Next(timeout time.Duration) (uint16, bool) {
	select {
	case code := <-r.events:
		// Swallow the repeats that follow a held key.
		deadline := time.After(250 * time.Millisecond)
		for {
			select {
			case <-r.events:
			case <-deadline:
				return code, true
			}
		}
	case <-time.After(timeout):
		return 0, false
	}
}

// Drain discards anything queued, so each prompt starts clean.
func (r *eventReader) Drain() {
	for {
		select {
		case <-r.events:
		default:
			r.drainTerminal()
			return
		}
	}
}

// quietenTerminal stops keypresses echoing and being buffered for the shell.
// ISIG is left alone, so Ctrl-C still interrupts.
func (r *eventReader) quietenTerminal() {
	if err := exec.Command("stty", "-F", "/dev/tty", "-echo", "-icanon").Run(); err != nil {
		return
	}
	r.ttyRestore = func() {
		_ = exec.Command("stty", "-F", "/dev/tty", "echo", "icanon").Run()
	}
	if tty, err := os.OpenFile("/dev/tty", os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		r.tty = tty
	}
}

func (r *eventReader) drainTerminal() {
	if r.tty == nil {
		return
	}
	buffer := make([]byte, 256)
	for {
		if n, err := r.tty.Read(buffer); err != nil || n == 0 {
			return
		}
	}
}

func (r *eventReader) Close() {
	for _, file := range r.grabbed {
		_ = ioctlGrab(file, 0)
	}
	for _, file := range r.files {
		_ = file.Close()
	}
	if r.tty != nil {
		r.drainTerminal()
		_ = r.tty.Close()
	}
	if r.ttyRestore != nil {
		r.ttyRestore()
	}
}
