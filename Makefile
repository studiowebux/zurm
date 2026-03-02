VERSION ?= dev
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build bundle install clean

build:
	go build $(LDFLAGS) -o zurm .

bundle: build
	rm -rf zurm.app
	mkdir -p zurm.app/Contents/MacOS
	mkdir -p zurm.app/Contents/Resources
	cp zurm zurm.app/Contents/MacOS/zurm
	cp macos/Info.plist zurm.app/Contents/Info.plist
	cp assets/icons/zurm.icns zurm.app/Contents/Resources/zurm.icns
	@echo "Built zurm.app"

install: bundle
	cp -r zurm.app /Applications/zurm.app
	@echo "Installed to /Applications/zurm.app"
	@echo "If macOS blocks the app, run: xattr -d com.apple.quarantine /Applications/zurm.app"

clean:
	rm -rf zurm zurm.app
