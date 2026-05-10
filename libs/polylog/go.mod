module github.com/thescaffold/gox-packages-polylog

go 1.25.3

replace github.com/thescaffold/gox-packages-core v0.0.0 => ../core

require (
	github.com/awesome-goose/goose v0.0.6
	github.com/thescaffold/gox-packages-core v0.0.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
