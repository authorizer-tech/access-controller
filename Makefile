download:
	@go mod download

install-tools: download
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % go install %