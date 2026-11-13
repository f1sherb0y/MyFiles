package index

// The attributes of files, as NTFS and Windows have them.
const (
	attrReadOnly  = 0x1
	attrHidden    = 0x2
	attrSystem    = 0x4
	attrDirectory = 0x10
	attrReparse   = 0x400
	attrOffline   = 0x1000
)

const (
	tagMountPoint = 0xA0000003
	tagSymlink    = 0xA000000C
	// attrRecallOnOpen is a folder whose entries are not on the disk: a
	// cloud's, which listing would fetch.
	attrRecallOnOpen       = 0x00040000
	attrRecallOnDataAccess = 0x00400000
)

// epoch is 1970 in the 100 ns of the system's times, which count from
// 1601.
const epoch = 116444736000000000

// unixSeconds is one of the system's times in seconds since 1970.
func unixSeconds(ft int64) uint32 {
	if ft <= epoch {
		return 0
	}
	s := (ft - epoch) / 10_000_000
	if s > 1<<32-1 {
		return 1<<32 - 1
	}
	return uint32(s)
}

// flagsOf is a file's flags, of its attributes and, for a reparse point,
// its tag.
func flagsOf(attrs, tag uint32) uint16 {
	var f uint16
	if attrs&attrDirectory != 0 {
		f |= FlagDir
	}
	if attrs&attrHidden != 0 {
		f |= FlagHidden
	}
	if attrs&attrSystem != 0 {
		f |= FlagSystem
	}
	if attrs&attrReadOnly != 0 {
		f |= FlagReadOnly
	}
	if attrs&attrReparse != 0 && (tag == tagMountPoint || tag == tagSymlink) {
		f |= FlagLink
	}
	if attrs&(attrOffline|attrRecallOnOpen|attrRecallOnDataAccess) != 0 {
		f |= FlagOffline
	}
	return f
}
