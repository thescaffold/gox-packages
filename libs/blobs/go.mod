module github.com/thescaffold/gox-packages-blobs

go 1.25.3

require github.com/awesome-goose/goose v0.0.6

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/thescaffold/gox-packages-core v0.0.0 => ../core
