# Cairn

一个快、紧凑的 Windows 文件管理器，用来替代资源管理器。界面用 [MyGo](https://github.com/egoist/mygo) 的原生 UI 在 GPU 上画，纯 Go，没有 cgo，单个 12 MB 的 exe。

- **快**：进一个文件夹，从发出导航到画面交给显示，本地磁盘上是 3–7 ms（5,000 项的 System32 约 6 ms，26,000 项的 WinSxS 约 25 ms）。同一台机器上 Explorer++ 是 53 ms–14.8 s，最小差距 16 倍；测法和完整数字见 [docs/benchmark.md](docs/benchmark.md)。
- **标签 + 分屏**：把标签拖到窗格的上、下、左、右边缘就分屏，拖到别的窗格就并过去，像窗口贴靠。
- **地址栏**：每一级路径是一个按钮；点空白处或按 Ctrl+L 变成输入框，可以输入、复制、粘贴。
- **文件操作**：标签之间、窗格之间、和别的程序之间拖放；Ctrl+C / X / V 和资源管理器互通。
- **多种视图**：详细信息、列表、平铺、中 / 大 / 超大图标。
- **安静**：没有导航的咔哒声，没有按键的叮声。
- **可以设成默认文件管理器**，也可以随时还原。

## 构建和运行

需要 Go 1.27。

```powershell
.\tools\setup-mygo.ps1      # 只需一次：取 MyGo 并打上 patches\mygo.patch
.\build.ps1                 # 生成 bin\cairn.exe
.\bin\cairn.exe D:\Code     # 打开一个文件夹
go test ./internal/...      # 测试（不弹窗口，不出声）
```

## 命令行

```
cairn [选项] [路径...]

  每个路径开一个标签；路径是文件就打开它所在的文件夹并选中它。
  不带路径启动时恢复上次的标签和分屏。

  -n, --new-window         开新窗口，而不是在已运行的窗口里加标签
      --select <路径>      打开所在文件夹并选中
      --split <right|down> 第二个路径起放到旁边的窗格
      --set-default        设为默认文件管理器
      --unset-default      还原
  -h, --help   -v, --version
```

explorer.exe 的参数也认，所以别的程序按资源管理器的方式调用也行：

```
cairn /select,D:\dir\file.txt
cairn /e,/root,D:\dir
cairn /n,D:\dir
cairn shell:Downloads
cairn file:///D:/dir
```

Cairn 是单实例的：再次启动只是把参数交给正在运行的那个。

## 快捷键

| | |
|---|---|
| Ctrl+T / Ctrl+W / Ctrl+Shift+T | 新标签 / 关标签 / 重新打开刚关的 |
| Ctrl+Tab、Ctrl+1…9 | 切换标签 |
| Ctrl+\ / Ctrl+Shift+\ | 向右 / 向下分屏 |
| F6 | 切换窗格 |
| Alt+← / Alt+→ / Alt+↑、Backspace | 后退 / 前进 / 上一级 |
| Ctrl+L、Alt+D、F4 | 编辑地址 |
| Ctrl+F、F3 | 过滤当前文件夹 |
| Ctrl+Shift+1…6、Ctrl+滚轮 | 详细信息 / 列表 / 平铺 / 中 / 大 / 超大图标 |
| Ctrl+C / X / V、Delete、Shift+Delete、F2 | 复制 / 剪切 / 粘贴、删除、永久删除、重命名 |
| Ctrl+Shift+N | 新建文件夹 |
| Ctrl+Shift+C | 复制路径 |
| Ctrl+H | 显示 / 隐藏隐藏文件 |
| Ctrl+B | 侧栏 |
| Ctrl+, | 设置 |
| Ctrl+N | 新窗口 |
| Alt+Enter | 属性 |

鼠标：中键点文件夹路径的某一级、侧栏的位置，在新标签里打开；中键点标签关闭它；右键菜单里的「更多选项…」是系统自己的右键菜单（7‑Zip 之类的扩展在那里）。

## 设为默认

设置（Ctrl+,）里有两个开关：「用 Cairn 打开文件夹」和「Win+E 打开 Cairn」。只改当前用户的注册表，不需要管理员权限；关掉时把原来的值原样写回。细节见 [docs/design.md](docs/design.md#默认文件管理器)。

## 目前没有的

- 回收站、控制面板、网络邻居这类不是目录的位置：遇到就交给资源管理器。
- 缩略图：图标视图里显示的是文件类型的图标，不是图片内容。
- 别的程序调用「在文件夹中显示并选中」（`SHOpenFolderAndSelectItems`）时，系统直接找资源管理器的窗口，不经过文件夹关联，所以仍然是资源管理器。
- exe 文件本身没有嵌图标（窗口和任务栏有）。
- macOS：代码按平台分好了层，能编译，但 `shell` 包在 macOS 上还是桩。

## 文档

- [docs/design.md](docs/design.md)：分层、为什么快、从 Explorer++ 学了什么、MyGo 的补丁。
- [docs/benchmark.md](docs/benchmark.md)：实测数字和测法。
