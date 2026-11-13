module myfiles

go 1.27.3

require (
	github.com/egoist/mygo v0.0.0-00010101000000-000000000000
	github.com/ledongthuc/pdf v0.0.0-20261020125853-5e46805f718e
	golang.org/x/image v0.46.0
	golang.org/x/sys v0.48.0
	golang.org/x/text v0.43.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/net v0.59.0 // indirect
)

replace github.com/egoist/mygo => ./third_party/mygo
