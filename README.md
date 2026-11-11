# Immich Mygo

Immich Mygo 是一个用 Go 和 MyGo 编写的 Immich 第三方桌面客户端，通过 Immich API 连接你自己的服务器。界面由 MyGo 原生 UI 绘制，不使用 WebView。

## 功能

- 按日期浏览照片和视频，并浏览收藏与相册。
- 按相册中最新照片的拍摄时间排序相册。
- 查看照片、视频和 Live Photo；长按 Live Photo 可播放动态画面。
- 查看资源详情，包括拍摄时间、相机、镜头、尺寸、位置和所属相册，并可从详情进入相册。
- 打开和退出照片查看器时，照片在缩略图与查看器之间缩放过渡。
- 显示用户头像，并记住照片详情面板的开关状态。

## 环境要求

- Go 1.27.3 或更新版本（见 `go.mod`）。
- 可访问的 Immich 服务器和有效账号。

## 运行

在仓库根目录执行：

```sh
go run .
```

首次启动时输入 Immich 服务器地址并登录。地址可以省略 `https://`，也可以包含 `/api`。

应用把访问令牌保存到操作系统的凭据存储中；会话元数据和界面偏好保存在 MyGo 的应用数据目录中。

## 开发

```sh
gofmt -w <修改过的 Go 文件>
go build ./...
go vet ./...
go tool mygo vet .
```

运行测试：

```sh
go test ./...
```

Immich API 客户端由 `openapi/immich-openapi-specs.json` 生成。更新 API 规范后，运行：

```sh
go generate ./immichapi
```

不要直接修改 `immichapi/immich.gen.go` 中的生成代码。

## 许可证

[MIT](LICENSE)
