package fsys

// VolumeKind is what a volume is on.
type VolumeKind uint8

const (
	VolumeFixed VolumeKind = iota
	VolumeRemovable
	VolumeNetwork
	VolumeOptical
)

// Volume is a drive, or another place files are kept under one root.
type Volume struct {
	// Root is the path of its top directory, as `C:\`.
	Root string
	// Name is what paths call it, as "C:".
	Name string
	// Label is the name the user gave it, which may be empty.
	Label      string
	FileSystem string
	Kind       VolumeKind
	// Ready is false for a drive without a medium, or one that does not
	// answer.
	Ready       bool
	Free, Total int64
}

// Title is the volume as file managers show it: "Data (D:)".
func (v *Volume) Title() string {
	label := v.Label
	if label == "" {
		switch v.Kind {
		case VolumeRemovable:
			label = "Removable Disk"
		case VolumeNetwork:
			label = "Network Drive"
		case VolumeOptical:
			label = "Optical Drive"
		default:
			label = "Local Disk"
		}
	}
	return label + " (" + v.Name + ")"
}

// PlaceKind is which of the user's folders a place is.
type PlaceKind uint8

const (
	PlaceHome PlaceKind = iota
	PlaceDesktop
	PlaceDownloads
	PlaceDocuments
	PlacePictures
	PlaceMusic
	PlaceVideos
)

// Place is one of the user's folders, as a sidebar lists them.
type Place struct {
	Kind PlaceKind
	Name string
	Path string
}
