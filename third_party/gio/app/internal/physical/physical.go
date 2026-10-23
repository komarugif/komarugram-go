// SPDX-License-Identifier: Unlicense OR MIT

// Package physical translates hardware positions to unshifted US key names.
// It does not translate text or depend on the active keyboard layout.
package physical

import (
	"gioui.org/io/key"
	"strconv"
	"strings"
)

func row(index int, s string) key.Name {
	if index < 0 || index >= len(s) {
		return ""
	}
	return key.Name(s[index : index+1])
}
func XKB(name string) key.Name {
	if len(name) == 4 {
		n, _ := strconv.Atoi(name[2:])
		switch name[:2] {
		case "AE":
			return row(n-1, "1234567890-=")
		case "AD":
			return row(n-1, "QWERTYUIOP[]")
		case "AC":
			return row(n-1, "ASDFGHJKL;'")
		case "AB":
			return row(n-1, "ZXCVBNM,./")
		case "FK":
			if n >= 1 && n <= 24 {
				return key.Name("F" + strconv.Itoa(n))
			}
		}
	}
	return map[string]key.Name{"TLDE": "`", "BKSL": "\\", "LSGT": "IntlBackslash", "ESC": key.NameEscape, "TAB": key.NameTab, "BKSP": key.NameDeleteBackward, "RTRN": key.NameReturn, "SPCE": key.NameSpace, "LEFT": key.NameLeftArrow, "RGHT": key.NameRightArrow, "UP": key.NameUpArrow, "DOWN": key.NameDownArrow, "HOME": key.NameHome, "END": key.NameEnd, "PGUP": key.NamePageUp, "PGDN": key.NamePageDown, "DELE": key.NameDeleteForward, "KPEN": key.NameEnter}[name]
}
func Windows(scan uint32, extended bool) key.Name {
	if extended {
		return map[uint32]key.Name{0x1c: key.NameEnter, 0x47: key.NameHome, 0x48: key.NameUpArrow, 0x49: key.NamePageUp, 0x4b: key.NameLeftArrow, 0x4d: key.NameRightArrow, 0x4f: key.NameEnd, 0x50: key.NameDownArrow, 0x51: key.NamePageDown, 0x53: key.NameDeleteForward}[scan]
	}
	switch {
	case scan >= 2 && scan <= 13:
		return row(int(scan)-2, "1234567890-=")
	case scan >= 16 && scan <= 27:
		return row(int(scan)-16, "QWERTYUIOP[]")
	case scan >= 30 && scan <= 40:
		return row(int(scan)-30, "ASDFGHJKL;'")
	case scan >= 44 && scan <= 53:
		return row(int(scan)-44, "ZXCVBNM,./")
	case scan >= 59 && scan <= 68:
		return key.Name("F" + strconv.Itoa(int(scan)-58))
	}
	return map[uint32]key.Name{1: key.NameEscape, 14: key.NameDeleteBackward, 15: key.NameTab, 28: key.NameReturn, 41: "`", 43: "\\", 57: key.NameSpace, 86: "IntlBackslash", 87: key.NameF11, 88: key.NameF12}[scan]
}

// Mac's sparse table is static so translating a key allocates nothing.
var macKeys = [128]key.Name{0: "A",
	1:   "S",
	2:   "D",
	3:   "F",
	4:   "H",
	5:   "G",
	6:   "Z",
	7:   "X",
	8:   "C",
	9:   "V",
	10:  "IntlBackslash",
	11:  "B",
	12:  "Q",
	13:  "W",
	14:  "E",
	15:  "R",
	16:  "Y",
	17:  "T",
	18:  "1",
	19:  "2",
	20:  "3",
	21:  "4",
	22:  "6",
	23:  "5",
	24:  "=",
	25:  "9",
	26:  "7",
	27:  "-",
	28:  "8",
	29:  "0",
	30:  "]",
	31:  "O",
	32:  "U",
	33:  "[",
	34:  "I",
	35:  "P",
	36:  key.NameReturn,
	37:  "L",
	38:  "J",
	39:  "'",
	40:  "K",
	41:  ";",
	42:  "\\",
	43:  ",",
	44:  "/",
	45:  "N",
	46:  "M",
	47:  ".",
	48:  key.NameTab,
	49:  key.NameSpace,
	50:  "`",
	51:  key.NameDeleteBackward,
	53:  key.NameEscape,
	76:  key.NameEnter,
	96:  key.NameF5,
	97:  key.NameF6,
	98:  key.NameF7,
	99:  key.NameF3,
	100: key.NameF8,
	101: key.NameF9,
	103: key.NameF11,
	109: key.NameF10,
	111: key.NameF12,
	115: key.NameHome,
	116: key.NamePageUp,
	117: key.NameDeleteForward,
	118: key.NameF4,
	119: key.NameEnd,
	120: key.NameF2,
	121: key.NamePageDown,
	122: key.NameF1,
	123: key.NameLeftArrow,
	124: key.NameRightArrow,
	125: key.NameDownArrow,
	126: key.NameUpArrow}

func Mac(code uint16) key.Name {
	if int(code) >= len(macKeys) {
		return ""
	}
	return macKeys[code]
}

func DOM(code string) key.Name {
	if strings.HasPrefix(code, "Key") && len(code) == 4 {
		return key.Name(code[3:])
	}
	if strings.HasPrefix(code, "Digit") && len(code) == 6 {
		return key.Name(code[5:])
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(code, "F")); strings.HasPrefix(code, "F") && err == nil && n >= 1 && n <= 24 {
		return key.Name(code)
	}
	return map[string]key.Name{"Minus": "-", "Equal": "=", "BracketLeft": "[", "BracketRight": "]", "Backslash": "\\", "IntlBackslash": "IntlBackslash", "Semicolon": ";", "Quote": "'", "Backquote": "`", "Comma": ",", "Period": ".", "Slash": "/", "Escape": key.NameEscape, "Tab": key.NameTab, "Backspace": key.NameDeleteBackward, "Delete": key.NameDeleteForward, "Enter": key.NameReturn, "NumpadEnter": key.NameEnter, "Space": key.NameSpace, "ArrowLeft": key.NameLeftArrow, "ArrowRight": key.NameRightArrow, "ArrowUp": key.NameUpArrow, "ArrowDown": key.NameDownArrow, "Home": key.NameHome, "End": key.NameEnd, "PageUp": key.NamePageUp, "PageDown": key.NamePageDown}[code]
}
