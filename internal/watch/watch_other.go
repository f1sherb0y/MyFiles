//go:build !windows

package watch

// Hub watches folders by the drives they are on. Where folders are not
// held by being watched, there is no need of it: no folder is its to
// watch.
type Hub struct{}

// New makes a Hub.
func New() *Hub { return &Hub{} }

// Watch reports false: the folder is to be watched some other way.
func (h *Hub) Watch(path string, notify func(Event)) (cancel func(), ok bool) {
	return nil, false
}

// Close ends every watch.
func (h *Hub) Close() {}
