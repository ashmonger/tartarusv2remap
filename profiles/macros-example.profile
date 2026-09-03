# What the keyd backend can do beyond one-key-to-one-key.
# `tartarus keys` lists every key name usable on the right-hand side;
# keyd(1) documents the full action syntax.

[profile]
name = Macro examples
extends = default

# Named macros keep [keys] readable and can be reused across profiles.
# A bare number followed by `ms` is a pause between presses.
[macros]
greet     = h e l l o
count     = 1 100ms 2 100ms 3
save_all  = C-s 200ms C-s

[keys]
k02 = C-c              # a chord: ctrl held while c is pressed
k03 = macro(C-x 50ms v) # an inline macro
k04 = @count            # a named macro from [macros] above
k05 = @save_all
k11 = @greet
