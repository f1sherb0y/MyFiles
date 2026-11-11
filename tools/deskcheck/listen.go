//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The sounds of Windows play in audio sessions of the default output
// device: one per program that plays, and one of the system's own, where
// the beeps and the clicks of the desktop play, whatever program asked
// for them. Each has a meter of the loudest sample it played lately.
// listen reads those meters every few milliseconds.

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procPlaySound        = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

	clsidDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidDeviceEnumerator   = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidSessionManager2    = windows.GUID{Data1: 0x77AA99A0, Data2: 0x1BD6, Data3: 0x484F, Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F}}
	iidSessionControl2    = windows.GUID{Data1: 0xBFB7FF88, Data2: 0x7239, Data3: 0x4FC9, Data4: [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D}}
	iidMeterInformation   = windows.GUID{Data1: 0xC02216F6, Data2: 0x8C67, Data3: 0x4B5B, Data4: [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64}}
)

// com is a COM object: a pointer to a pointer to its methods.
type com struct{ p *comObject }

type comObject struct{ methods *[64]uintptr }

func (o com) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.p.methods[method], append([]uintptr{uintptr(unsafe.Pointer(o.p))}, args...)...)
	return r
}

func (o com) release() {
	if o.p != nil {
		o.call(2)
	}
}

// query is the object as another of its interfaces, 0 when it has none.
func (o com) query(iid *windows.GUID) com {
	var out com
	if int32(o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) < 0 {
		return com{}
	}
	return out
}

// session is an audio session with its meter.
type session struct {
	meter  com
	pid    uint32
	system bool
}

// sessions lists the sessions of the default output device.
func sessions(manager com) []session {
	var enum com
	if int32(manager.call(5, uintptr(unsafe.Pointer(&enum)))) < 0 {
		return nil
	}
	defer enum.release()
	var n int32
	enum.call(3, uintptr(unsafe.Pointer(&n)))
	var out []session
	for i := int32(0); i < n; i++ {
		var ctl com
		if int32(enum.call(4, uintptr(i), uintptr(unsafe.Pointer(&ctl)))) < 0 {
			continue
		}
		s := session{meter: ctl.query(&iidMeterInformation)}
		if ctl2 := ctl.query(&iidSessionControl2); ctl2.p != nil {
			ctl2.call(14, uintptr(unsafe.Pointer(&s.pid)))
			s.system = ctl2.call(15) == 0
			ctl2.release()
		}
		ctl.release()
		if s.meter.p != nil {
			out = append(out, s)
		}
	}
	return out
}

func peak(meter com) float32 {
	var v float32
	meter.call(3, uintptr(unsafe.Pointer(&v)))
	return v
}

// quietTone is a sound too quiet to hear: 0.3 s of a 1 kHz square wave of
// the least amplitude 16 bits have, 90 dB under full scale.
func quietTone() []byte {
	const rate, samples = 44100, 44100 * 3 / 10
	b := make([]byte, 44+2*samples)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2*samples)
	for i := 0; i < samples; i++ {
		v := int16(1)
		if i/22%2 == 1 {
			v = -1
		}
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// listen reports the loudest sample played while it listens: by the
// system's own session, by the process pid, by this program (the control)
// and by everything else.
func listen(pid uint32, d time.Duration, control, asSystem bool) error {
	runtime.LockOSThread()
	procCoInitializeEx.Call(0, 0)
	var enumerator com
	const clsctxAll = 0x17
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator))); int32(hr) < 0 {
		return fmt.Errorf("no audio device enumerator: %#x", uint32(hr))
	}
	var device com
	if hr := enumerator.call(4, 0, 0, uintptr(unsafe.Pointer(&device))); int32(hr) < 0 {
		return fmt.Errorf("no default output device: %#x", uint32(hr))
	}
	var manager, total com
	if hr := device.call(3, uintptr(unsafe.Pointer(&iidSessionManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&manager))); int32(hr) < 0 {
		return fmt.Errorf("no session manager: %#x", uint32(hr))
	}
	device.call(3, uintptr(unsafe.Pointer(&iidMeterInformation)), clsctxAll, 0, uintptr(unsafe.Pointer(&total)))

	self := uint32(os.Getpid())
	var tone []byte
	if control {
		tone = quietTone()
	}
	var system, watched, own, others, device0 float32
	systemSeen, watchedSeen := false, false
	var list []session
	start := time.Now()
	played := false
	for n := 0; time.Since(start) < d; n++ {
		if n%12 == 0 {
			for _, s := range list {
				s.meter.release()
			}
			list = sessions(manager)
		}
		if control && !played && time.Since(start) > d/2 {
			played = true
			// As a sound of the system's, it plays in the session the
			// beeps of the desktop play in, which is the one to check.
			const sndAsync, sndNoDefault, sndMemory, sndSystem = 0x1, 0x2, 0x4, 0x00200000
			flags := uintptr(sndAsync | sndNoDefault | sndMemory)
			if asSystem {
				flags |= sndSystem
			}
			procPlaySound.Call(uintptr(unsafe.Pointer(&tone[0])), 0, flags)
		}
		for _, s := range list {
			v := peak(s.meter)
			switch {
			case s.system:
				systemSeen = true
				system = max(system, v)
			case s.pid == pid && pid != 0:
				watchedSeen = true
				watched = max(watched, v)
			case s.pid == self:
				own = max(own, v)
			default:
				others = max(others, v)
			}
		}
		if total.p != nil {
			device0 = max(device0, peak(total))
		}
		time.Sleep(8 * time.Millisecond)
	}
	runtime.KeepAlive(tone)
	fmt.Printf("listened for %v on the default output device\n", d)
	fmt.Printf("system sounds session: found %v, loudest %.6f\n", systemSeen, system)
	fmt.Printf("process %d: session found %v, loudest %.6f\n", pid, watchedSeen, watched)
	if control {
		where := "in this program's own session"
		if asSystem {
			where = "played as a system sound: see the system sounds session"
		}
		fmt.Printf("control (a tone 90 dB under full scale, %s): own session loudest %.6f\n", where, own)
	}
	fmt.Printf("other programs: loudest %.6f; the device as a whole: %.6f\n", others, device0)
	return nil
}
