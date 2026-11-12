package shell

import (
	"fmt"

	"myfiles/internal/fsys"
)

var iidObjectWithProgID = guid("71e806fb-8dee-46fc-bf8c-7748a8a1ae13")

func handlerProgID(h *object) string {
	p := h.query(&iidObjectWithProgID)
	if p == nil {
		return ""
	}
	defer p.release()
	const getProgID = 4
	return handlerString(p, getProgID)
}

// appProgID asks the selected handler for its registration, preserving its
// command, arguments and packaged-app activation instead of guessing from
// the executable name.
func appProgID(path string, app App) (string, error) {
	ext := fsys.ExtOf(fsys.Base(path))
	if ext == "" {
		return "", fmt.Errorf("the file has no extension")
	}
	var id string
	err := onSTA(func() error {
		found := false
		find := func(h *object) bool {
			if handlerString(h, handlerGetName) != app.ID {
				return true
			}
			found = true
			id = handlerProgID(h)
			return false
		}
		err := handlers(ext, false, find)
		if err == nil && !found {
			err = handlers(ext, true, find)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no file association is registered for %q", app.ID)
	}
	return id, nil
}

func setDefaultApp(path string, app App) error {
	id, err := appProgID(path, app)
	if err != nil {
		return err
	}
	return setDefaultProgID(fsys.ExtOf(fsys.Base(path)), id)
}
