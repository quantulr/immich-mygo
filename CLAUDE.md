# Claude Code 项目指南

## 项目约束

- 本项目使用 Go 和 `github.com/egoist/mygo` 构建 Immich 桌面客户端，界面必须保持为 MyGo 原生 UI。任何情况下都不要引入或使用 WebView。
- 与用户沟通及新增项目文档使用中文；代码标识符保留英文。
- 保留工作区里已有的用户改动，不要擅自重置或覆盖。
- 只有用户明确要求时才添加或运行测试；只有用户明确要求时才提交或推送。合并分支也需要用户明确要求。

## 项目结构

- `main.go`：应用入口、窗口配置和主视图分派。
- `mainui.go`：登录后的主窗口、侧边栏、照片/收藏/相册页面，以及时间线网格。
- `timeline.go`：Immich 时间线、相册、缩略图和资源详情的数据请求与转换。
- `viewer.go`：照片查看器、详情面板、照片导航，以及缩略图与查看器之间的缩放过渡。
- `video.go`：视频与 Live Photo 播放。
- `login.go`、`session.go`：服务器连接、登录、会话恢复及凭据存储。
- `prefs.go`：界面偏好持久化，例如详情面板的默认状态。
- `thumbs.go`：受内存预算约束的缩略图缓存及并发加载限制。
- `openapi/immich-openapi-specs.json`：Immich OpenAPI 规范快照。
- `immichapi/immich.gen.go`：由 OpenAPI 规范生成的 API 客户端；不要手工编辑。

## 开发与验证

项目使用 `go.mod` 指定的 Go 版本和 MyGo 版本。修改 MyGo UI、生命周期或焦点行为前，先阅读 `.agents/skills/mygo-maintenance/SKILL.md`，并核对当前锁定版本的 API。

常用命令：

```sh
gofmt -w <修改过的 Go 文件>
go build ./...
go vet ./...
go tool mygo vet .
go test ./...
```

遵循用户要求，除非用户明确要求测试或验证，否则不要添加或运行测试。修改 `openapi/immich-openapi-specs.json` 后，用 `go generate ./immichapi` 重新生成客户端，不要直接编辑生成文件。

## MyGo UI 约定

- 将应用数据和控件状态保存在应用状态中；不要跨构建帧保存 `ui.Element` 或 `ui.Context`。
- 对有状态控件使用稳定且唯一的 `c.Key(...)`；处理展开、隐藏和重建时留意控件身份与焦点状态。
- 网络请求和耗时工作放在 UI 线程之外，通过 `Window.Update` 发布结果。
- 优先使用 MyGo 控件，保留键盘操作、焦点和辅助功能行为；沿用应用的主题、尺寸和动效风格。
- 缩略图与照片查看器之间的过渡使用 `ui.ElementTransition` 的 `Match`、`Fly` 和 `ScaleContent`；两处图片共用 `photoMatch` 身份。
