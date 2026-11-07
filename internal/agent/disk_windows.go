package agent

import (
	"golang.org/x/sys/windows"
	"math"
	"runtime"
	"unsafe"
)

var pdh = windows.NewLazySystemDLL("pdh.dll")
var pdhOpen = pdh.NewProc("PdhOpenQueryW")
var pdhAdd = pdh.NewProc("PdhAddEnglishCounterW")
var pdhCollect = pdh.NewProc("PdhCollectQueryData")
var pdhValue = pdh.NewProc("PdhGetFormattedCounterValue")
var pdhClose = pdh.NewProc("PdhCloseQuery")

type diskQuery struct{ query, read, write uintptr }

func newDiskQuery() *diskQuery {
	d := &diskQuery{}
	if code, _, _ := pdhOpen.Call(0, 0, uintptr(unsafe.Pointer(&d.query))); code != 0 {
		return nil
	}
	for path, target := range map[string]*uintptr{`\PhysicalDisk(_Total)\Disk Read Bytes/sec`: &d.read, `\PhysicalDisk(_Total)\Disk Write Bytes/sec`: &d.write} {
		name, _ := windows.UTF16PtrFromString(path)
		if code, _, _ := pdhAdd.Call(d.query, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(target))); code != 0 {
			pdhClose.Call(d.query)
			return nil
		}
	}
	runtime.AddCleanup(d, func(query uintptr) { pdhClose.Call(query) }, d.query)
	return d
}

func (d *diskQuery) sample() (*float64, *float64) {
	if d == nil {
		return nil, nil
	}
	defer runtime.KeepAlive(d)
	if code, _, _ := pdhCollect.Call(d.query); code != 0 {
		return nil, nil
	}
	read := func(counter uintptr) *float64 {
		var value struct {
			Status uint32
			_      uint32
			Value  float64
		}
		code, _, _ := pdhValue.Call(counter, 0x200, 0, uintptr(unsafe.Pointer(&value)))
		if code != 0 || value.Status > 1 || math.IsNaN(value.Value) || math.IsInf(value.Value, 0) || value.Value < 0 || value.Value > 1e13 {
			return nil
		}
		return &value.Value
	}
	return read(d.read), read(d.write)
}
