package shell

import (
	"encoding/binary"

	"golang.org/x/sys/windows"
)

// cutFormat is the clipboard format Explorer reads to tell a cut from a
// copy: one DROPEFFECT, as 32 bits.
const cutFormat = "Preferred DropEffect"

// The effects of a drop (DROPEFFECT_).
const (
	dropEffectCopy = 1
	dropEffectMove = 2
	dropEffectLink = 4
)

func cutData(cut bool) []byte {
	effect := uint32(dropEffectCopy | dropEffectLink)
	if cut {
		effect = dropEffectMove
	}
	return binary.LittleEndian.AppendUint32(nil, effect)
}

func isCut(data []byte) bool {
	return len(data) >= 4 && binary.LittleEndian.Uint32(data)&dropEffectMove != 0
}

var procCoInternetSetFeatureEnabled = windows.NewLazySystemDLL("urlmon.dll").NewProc("CoInternetSetFeatureEnabled")

// quiet turns off the click the shell's own views and the web controls
// make when they go to a folder or a page in this process. The system has
// no switch for the beeps of unhandled keys: a window makes none when it
// handles the key messages that would.
func quiet() {
	const (
		featureDisableNavigationSounds = 21
		setFeatureOnProcess            = 0x2
	)
	if procCoInternetSetFeatureEnabled.Find() != nil {
		return
	}
	procCoInternetSetFeatureEnabled.Call(featureDisableNavigationSounds, setFeatureOnProcess, 1)
}
