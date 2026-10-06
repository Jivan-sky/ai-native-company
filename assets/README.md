# assets

README 题图的源文件与构建方式。

| 文件 | 说明 |
|---|---|
| `background.jpg` | 背景原稿（1920×960，2:1）。由原始插图居中裁成 2:1 后缩放，未做其它处理 |
| `cover.svg` | 题图源文件。相对引用 `background.jpg`；面板的玻璃质感由 SVG 滤镜实现（对背景做模糊 + 饱和 + 亮度提升，再叠高光边与投影） |
| `cover.jpg` | README 实际引用的成品（1920×960，JPEG q90） |

## 重新生成 cover.jpg

需要 Chromium 内核浏览器（Edge / Chrome 任一）与 Pillow。

**光栅化时必须直接打开 SVG**，不能把 SVG 放进 `<img>` 标签里——静态图片模式会禁止加载外部引用的 `background.jpg`，背景会变成空白。

```powershell
# $edge 换成自己机器上的浏览器可执行文件；SVG 用本机仓库绝对路径
& $edge --headless=new --disable-gpu --hide-scrollbars `
  --force-device-scale-factor=1.5 --window-size=1280,640 `
  --screenshot="cover-render.png" `
  "file:///<仓库绝对路径>/assets/cover.svg"
```

再把 PNG 转成 JPEG（quality 90）存为 `cover.jpg`。走 JPEG 只为体积：PNG 直出 1.77 MB，JPEG 0.29 MB，而 README 的显示宽度下肉眼无差。
