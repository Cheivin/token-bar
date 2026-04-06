APP_NAME    := Token-Bar
BINARY      := token-bar
VERSION     := 0.0.2
BUNDLE_ID   := com.cheivin.token-bar

APP_DIR     := $(APP_NAME).app
CONTENTS    := $(APP_DIR)/Contents
MACOS       := $(CONTENTS)/MacOS
RESOURCES   := $(CONTENTS)/Resources
ICONSET     := packaging/$(APP_NAME).iconset
ICNS        := packaging/icon.icns
DIST_ZIP    := dist/$(APP_NAME)-$(VERSION).zip

.PHONY: build build-universal icon app package clean release

# 编译当前架构二进制
build:
	go build -o $(BINARY) .

# 编译通用二进制（同时支持 Apple Silicon 和 Intel）
build-universal:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -target arm64-apple-macos11" go build -o $(BINARY)_arm64 .
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -target x86_64-apple-macos10.12" go build -o $(BINARY)_amd64 .
	lipo -create -output $(BINARY) $(BINARY)_arm64 $(BINARY)_amd64
	@rm -f $(BINARY)_arm64 $(BINARY)_amd64
	@echo "已生成通用二进制 $(BINARY)"

# 从 JPG 生成 .icns 图标
icon:
	@mkdir -p $(ICONSET)
	sips -s format png -z 16 16 packaging/icon.jpg --out $(ICONSET)/icon_16x16.png
	sips -s format png -z 32 32 packaging/icon.jpg --out $(ICONSET)/icon_16x16@2x.png
	sips -s format png -z 32 32 packaging/icon.jpg --out $(ICONSET)/icon_32x32.png
	sips -s format png -z 64 64 packaging/icon.jpg --out $(ICONSET)/icon_32x32@2x.png
	sips -s format png -z 128 128 packaging/icon.jpg --out $(ICONSET)/icon_128x128.png
	sips -s format png -z 256 256 packaging/icon.jpg --out $(ICONSET)/icon_128x128@2x.png
	sips -s format png -z 256 256 packaging/icon.jpg --out $(ICONSET)/icon_256x256.png
	sips -s format png -z 512 512 packaging/icon.jpg --out $(ICONSET)/icon_256x256@2x.png
	sips -s format png -z 512 512 packaging/icon.jpg --out $(ICONSET)/icon_512x512.png
	sips -s format png -z 1024 1024 packaging/icon.jpg --out $(ICONSET)/icon_512x512@2x.png
	iconutil -c icns $(ICONSET) -o $(ICNS)
	@rm -rf $(ICONSET)
	@echo "已生成 $(ICNS)"

# 创建 .app 包（通用二进制）
app: build-universal icon
	@mkdir -p $(MACOS) $(RESOURCES)
	@cp $(BINARY) $(MACOS)/$(BINARY)
	@cp packaging/Info.plist $(CONTENTS)/Info.plist
	@cp $(ICNS) $(RESOURCES)/icon.icns
	@echo "已创建 $(APP_DIR)"

# 打包为 zip
package: app
	@mkdir -p dist
	@rm -f $(DIST_ZIP)
	@cd . && zip -r $(DIST_ZIP) $(APP_DIR)
	@echo "已打包 $(DIST_ZIP)"

clean:
	rm -rf $(APP_DIR) dist $(BINARY) $(BINARY)_arm64 $(BINARY)_amd64 $(ICNS) packaging/*.iconset
