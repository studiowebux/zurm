VERSION ?= dev
LDFLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build build-server bundle dmg install clean

build:
	go build $(LDFLAGS) -o zurm .

build-server:
	go build $(LDFLAGS) -o zurm-server ./cmd/zurm-server

bundle: build
	rm -rf zurm.app
	mkdir -p zurm.app/Contents/MacOS
	mkdir -p zurm.app/Contents/Resources
	cp zurm zurm.app/Contents/MacOS/zurm
	cp macos/Info.plist zurm.app/Contents/Info.plist
	cp assets/icons/zurm.icns zurm.app/Contents/Resources/zurm.icns
	sed -i '' 's/__VERSION__/$(VERSION)/' zurm.app/Contents/Info.plist
	sed -i '' 's/__BUILD__/$(VERSION)/' zurm.app/Contents/Info.plist
	codesign --sign - --force --deep zurm.app
	@echo "Built and signed zurm.app (ad-hoc)"

dmg: bundle
	rm -f zurm-macos-arm64.dmg
	hdiutil create -volname "Zurm" -srcfolder zurm.app -ov -format UDZO zurm-macos-arm64.dmg
	@echo "Created zurm-macos-arm64.dmg"

install: bundle
	cp -r zurm.app /Applications/zurm.app
	/System/Library/Frameworks/CoreServices.framework/Versions/Current/Frameworks/LaunchServices.framework/Versions/Current/Support/lsregister -f /Applications/zurm.app
	@echo "Installed to /Applications/zurm.app (registered with LaunchServices)"

clean:
	rm -rf zurm zurm.app zurm-macos-arm64.dmg
