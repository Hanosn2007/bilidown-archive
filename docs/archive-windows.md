# Windows 11 归档初版跑测

## 环境

- Go 1.23+
- Node.js + pnpm
- FFmpeg，任选其一：
  - 将 `ffmpeg.exe` 加入 `PATH`
  - 或放到 `server\bin\ffmpeg.exe`

## 启动

```powershell
cd client
pnpm install
pnpm build

cd ..\server
go mod tidy
go build
.\bilidown.exe
```

打开 `http://127.0.0.1:8098`，扫码登录后进入“归档库”。

## 开机自启动

初版先按用户态应用运行，不注册 Windows 服务。测试稳定后可把 `server\bilidown.exe` 的快捷方式放入：

```text
shell:startup
```

注意快捷方式的“起始位置”要设为 `server` 目录，否则程序会找不到 `static`、`data.db` 或 `bin\ffmpeg.exe`。

## 测试流程

1. 在 B 站新建一个专用收藏夹。
2. 从收藏夹 URL 中取 `fid`，填入“收藏夹 media_id”。
3. 保存设置。
4. 点“立即扫描”。
5. 归档库出现记录后，任务列表会开始下载。
6. 下载完成后，在归档库点条目可本地播放，并可打开 `info.json` 和弹幕 XML。

清晰度选择“最高可用”时会优先使用 B 站返回的最高格式，包括 8K、杜比视界、HDR、4K、1080P60 等。选择固定清晰度但单个视频不可用时，会自动降级到该视频可用的下一档。Hi-Res 开启时优先使用 flac 音频，没有 flac 时回退到最高普通音频。

## 文件布局

视频文件仍在下载目录根部，归档信息保存在：

```text
下载目录\_archive\BVxxxx\001\
  info.json
  cover.jpg
  danmaku.xml
```

`info.json` 是主要信息快照，保存视频信息、分 P 信息、抓取时间、原链接和下载配置。
