package main

// Generated from linux/input-event-codes.h: keyboard keys only, meaning codes
// 1-127 plus F13-F24. Codes are written out rather than counted, because the
// range has a hole at 84. Mice, gamepads and media appliances are excluded.
const keyboardKeys = "" +
	"esc:1 1:2 2:3 3:4 4:5 5:6 6:7 7:8 8:9 9:10 0:11 minus:12 equal:13 backspace:14 tab:15 " +
	"q:16 w:17 e:18 r:19 t:20 y:21 u:22 i:23 o:24 p:25 leftbrace:26 rightbrace:27 enter:28 " +
	"leftctrl:29 a:30 s:31 d:32 f:33 g:34 h:35 j:36 k:37 l:38 semicolon:39 apostrophe:40 " +
	"grave:41 leftshift:42 backslash:43 z:44 x:45 c:46 v:47 b:48 n:49 m:50 comma:51 dot:52 " +
	"slash:53 rightshift:54 kpasterisk:55 leftalt:56 space:57 capslock:58 f1:59 f2:60 f3:61 " +
	"f4:62 f5:63 f6:64 f7:65 f8:66 f9:67 f10:68 numlock:69 scrolllock:70 kp7:71 kp8:72 " +
	"kp9:73 kpminus:74 kp4:75 kp5:76 kp6:77 kpplus:78 kp1:79 kp2:80 kp3:81 kp0:82 kpdot:83 " +
	"zenkakuhankaku:85 102nd:86 f11:87 f12:88 ro:89 katakana:90 hiragana:91 henkan:92 " +
	"katakanahiragana:93 muhenkan:94 kpjpcomma:95 kpenter:96 rightctrl:97 kpslash:98 " +
	"sysrq:99 rightalt:100 linefeed:101 home:102 up:103 pageup:104 left:105 right:106 " +
	"end:107 down:108 pagedown:109 insert:110 delete:111 macro:112 mute:113 volumedown:114 " +
	"volumeup:115 power:116 kpequal:117 kpplusminus:118 pause:119 scale:120 kpcomma:121 " +
	"hangeul:122 hanja:123 yen:124 leftmeta:125 rightmeta:126 compose:127 f13:183 f14:184 " +
	"f15:185 f16:186 f17:187 f18:188 f19:189 f20:190 f21:191 f22:192 f23:193 f24:194 "

// Input is one physical input of the keypad.
//
//	Label     the name a profile uses
//	Stock     the key the keypad sends before remapping; keyd matches on this
//	Scancode  the HID scancode, for the legacy udev hwdb backend
type Input struct {
	Label    string
	Stock    string
	Scancode string
}

// Razer counts 19 backlit keys, a Hyperesponse thumb key, a spacebar actuator
// and an 8-way thumb pad. The pad reports diagonals as two cardinals at once,
// so only the four cardinals are bindable.
var layout = []Input{
	{"k01", "1", "7001e"}, {"k02", "2", "7001f"}, {"k03", "3", "70020"},
	{"k04", "4", "70021"}, {"k05", "5", "70022"},
	{"k06", "tab", "7002b"}, {"k07", "q", "70014"}, {"k08", "w", "7001a"},
	{"k09", "e", "70008"}, {"k10", "r", "70015"},
	{"k11", "capslock", "70039"}, {"k12", "a", "70004"}, {"k13", "s", "70016"},
	{"k14", "d", "70007"}, {"k15", "f", "70009"},
	{"k16", "leftshift", "700e1"}, {"k17", "z", "7001d"}, {"k18", "x", "7001b"},
	{"k19", "c", "70006"},
	{"thumb", "leftalt", "700e2"},
	{"pad_up", "up", "70052"}, {"pad_down", "down", "70051"},
	{"pad_left", "left", "70050"}, {"pad_right", "right", "7004f"},
	{"bar", "space", "7002c"},
}

// The keypad's own vendor:product. lsusb or `keyd monitor` confirms it.
const defaultDevice = "1532:022b"

// gridRows are the four rows of backlit keys; the thumb cluster is drawn apart.
var gridRows = [][]string{
	{"k01", "k02", "k03", "k04", "k05"},
	{"k06", "k07", "k08", "k09", "k10"},
	{"k11", "k12", "k13", "k14", "k15"},
	{"k16", "k17", "k18", "k19"},
}

var thumbCluster = []string{"thumb", "pad_up", "pad_down", "pad_left", "pad_right", "bar"}
