# macOS 实体机运行说明

这个版本仍然是 Web 前端加本地 Go 后端，不改成桌面应用壳子。macOS 实体机运行时，播放器优先直接读取原始归档文件；只有浏览器无法播放原始编码时，前端才会请求 `/api/archive/preview` 生成 H.264/AAC 预览缓存。

## 播放策略

- 优先使用原文件：`/api/downloadVideo?path=...`
- 原文件播放失败后再生成预览：`/api/archive/preview?path=...`
- 预览缓存放在下载目录的 `_preview` 里，可以在归档库页面手动删除
- Safari 通常比 Chrome/Edge 更适合直接播放 HEVC/HDR 文件

## FFmpeg

启动应用不再强制要求 FFmpeg。没有 FFmpeg 时，已经归档的浏览器可播文件仍可直接播放。

这些功能仍然需要 FFmpeg：

- 下载完成后的音视频合并
- 写入媒体元数据
- 浏览器无法播放原文件时生成预览缓存

推荐安装：

```bash
brew install ffmpeg
```

也可以把可执行文件放到 `server/bin/ffmpeg` 或最终程序目录的 `bin/ffmpeg`。

## macOS 预览转码

在 macOS 上生成预览时，后端会优先尝试 `h264_videotoolbox`，让 FFmpeg 使用系统硬件编码；如果失败，会自动回退到软件 `libx264`。

这只影响“浏览器播不了原文件时的兜底转码”，不会改变原始归档文件。

## 像 Windows 托盘版一样启动

执行一次：

```bash
./scripts/build-macos-app.sh
```

脚本会构建前端、编译后端，并生成：

```text
server/Bilidown.app
```

之后双击 `server/Bilidown.app` 即可后台运行，不会留下终端窗口。应用只显示菜单栏图标，并会自动打开 `http://127.0.0.1:8098`。首次启动到浏览器可访问可能需要十几秒。

这个 app 的工作目录固定为 `server/`，所以数据仍然在：

```text
server/data.db
server/download
server/static
```

从 Windows 虚拟机迁移过来的 `data.db` 和 `download` 也放到这里。
