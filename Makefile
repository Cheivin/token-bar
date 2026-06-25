APP_NAME    := Token-Bar
BINARY      := token-bar
VERSION     := 0.0.3
BUNDLE_ID   := com.cheivin.token-bar

# 单架构产物目录（.app + Applications 快捷方式）
STAGE_ARM   := build/arm64
STAGE_AMD   := build/amd64
APP_DIR_ARM := $(STAGE_ARM)/$(APP_NAME).app
APP_DIR_AMD := $(STAGE_AMD)/$(APP_NAME).app

# 兼容旧 app 目标的通用二进制 .app
APP_DIR     := $(APP_NAME).app
CONTENTS    := $(APP_DIR)/Contents
MACOS       := $(CONTENTS)/MacOS
RESOURCES   := $(CONTENTS)/Resources

ICONSET     := packaging/$(APP_NAME).iconset
ICNS        := packaging/icon.icns

DIST_DMG_ARM := dist/$(APP_NAME)-$(VERSION)-arm64.dmg
DIST_DMG_AMD := dist/$(APP_NAME)-$(VERSION)-amd64.dmg
DIST_ZIP    := dist/$(APP_NAME)-$(VERSION).zip

# 绕过 Go 1.25+ arm64 编译器优化器 bug（progrium/darwinkit issue #286）：
# 优化器为 darwinkit 的 libffi cgo 调用生成错误代码，导致 Application.Run() 运行时 SIGABRT。
# 关闭优化(-N)与内联(-l)可规避。详见 https://github.com/progrium/darwinkit/issues/286
GCFLAGS     := -gcflags="all=-N -l"

.PHONY: build build-arm64 build-amd64 build-universal icon app app-arm64 app-amd64 dmg-arm64 dmg-amd64 package clean release

# 编译当前架构二进制
build:
	go build $(GCFLAGS) -o $(BINARY) .

# 编译 Apple Silicon（arm64）二进制
build-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -target arm64-apple-macos11" go build $(GCFLAGS) -o $(BINARY)_arm64 .

# 编译 Intel（amd64）二进制
build-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -target x86_64-apple-macos10.12" go build $(GCFLAGS) -o $(BINARY)_amd64 .

# 编译通用二进制（同时支持 Apple Silicon 和 Intel）
build-universal: build-arm64 build-amd64
	lipo -create -output $(BINARY) $(BINARY)_arm64 $(BINARY)_amd64
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

# 创建 arm64 .app 包
app-arm64: build-arm64 icon
	@mkdir -p $(APP_DIR_ARM)/Contents/MacOS $(APP_DIR_ARM)/Contents/Resources
	@cp $(BINARY)_arm64 $(APP_DIR_ARM)/Contents/MacOS/$(BINARY)
	@cp packaging/Info.plist $(APP_DIR_ARM)/Contents/Info.plist
	@cp $(ICNS) $(APP_DIR_ARM)/Contents/Resources/icon.icns
	@echo "已创建 $(APP_DIR_ARM)"

# 创建 amd64 .app 包
app-amd64: build-amd64 icon
	@mkdir -p $(APP_DIR_AMD)/Contents/MacOS $(APP_DIR_AMD)/Contents/Resources
	@cp $(BINARY)_amd64 $(APP_DIR_AMD)/Contents/MacOS/$(BINARY)
	@cp packaging/Info.plist $(APP_DIR_AMD)/Contents/Info.plist
	@cp $(ICNS) $(APP_DIR_AMD)/Contents/Resources/icon.icns
	@echo "已创建 $(APP_DIR_AMD)"

# 兼容旧目标：创建通用二进制 .app 包
app: build-universal icon
	@mkdir -p $(MACOS) $(RESOURCES)
	@cp $(BINARY) $(MACOS)/$(BINARY)
	@cp packaging/Info.plist $(CONTENTS)/Info.plist
	@cp $(ICNS) $(RESOURCES)/icon.icns
	@echo "已创建 $(APP_DIR)"

# 打包 Apple Silicon dmg（含 /Applications 快捷方式，挂载后拖拽安装）
dmg-arm64: app-arm64
	@mkdir -p dist
	@rm -f $(DIST_DMG_ARM)
	@ln -sf /Applications $(STAGE_ARM)/Applications
	hdiutil create -volname "$(APP_NAME)" -srcfolder $(STAGE_ARM) -ov -format UDZO "$(DIST_DMG_ARM)"
	@rm -f $(STAGE_ARM)/Applications
	@echo "已打包 $(DIST_DMG_ARM)"

# 打包 Intel dmg（含 /Applications 快捷方式，挂载后拖拽安装）
dmg-amd64: app-amd64
	@mkdir -p dist
	@rm -f $(DIST_DMG_AMD)
	@ln -sf /Applications $(STAGE_AMD)/Applications
	hdiutil create -volname "$(APP_NAME)" -srcfolder $(STAGE_AMD) -ov -format UDZO "$(DIST_DMG_AMD)"
	@rm -f $(STAGE_AMD)/Applications
	@echo "已打包 $(DIST_DMG_AMD)"

# 打包两个架构的 dmg
package: dmg-arm64 dmg-amd64

clean:
	rm -rf $(APP_DIR) build dist $(BINARY) $(BINARY)_arm64 $(BINARY)_amd64 $(ICNS) packaging/*.iconset
