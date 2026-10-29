// Package sys groups the Windows-specific calls (with stand-in versions for
// other systems, only so the code can be tested on Linux).
package sys

// MessageBox flags.
const (
	MBOK           = 0x0
	MBYesNo        = 0x4
	MBIconError    = 0x10
	MBIconQuestion = 0x20
	MBIconWarning  = 0x30
	MBIconInfo     = 0x40
	IDYes          = 6
)
