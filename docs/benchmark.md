# 实测：MyFiles 和 Explorer++ 进文件夹的速度

2026-11-12 在这台机器上测（Ryzen AI 9 HX 370，Windows 11 26300，2560×1600 @ 165 Hz，125% 缩放）。MyFiles 0.1.0，Explorer++ 1.4.0.2293（winget 装的那个版本）。

**结论：在同一批文件夹上，MyFiles 进文件夹的中位耗时是 3–25 ms，Explorer++ 是 53 ms–14.8 s；启动到第一个文件夹显示，MyFiles 166 ms，Explorer++ 330 ms。**

## 进文件夹

每个文件夹进 5 次，取中位数，单位毫秒。测试在同一个标签里按顺序从一个文件夹走到下一个，每次显示完停 250 ms。

| 文件夹 | 条目数 | MyFiles | Explorer++ 默认（图标视图） | 倍数 | Explorer++ 详细信息视图 | 倍数 |
|---|---:|---:|---:|---:|---:|---:|
| D:\Code | 56 | 3.25 | 53.3 | 16× | 59.2 | 18× |
| C:\Windows | 118 | 4.00 | 73.5 | 18× | 68.9 | 17× |
| C:\Program Files | 118 | 3.91 | 71.0 | 18× | 63.9 | 16× |
| C:\Users\fish | 166 | 3.41 | 98.9 | 29× | 80.5 | 24× |
| C:\Windows\System32\drivers | 513 | 3.41 | 191 | 56× | 128 | 37× |
| C:\Windows\Fonts | 598 | 4.35 | 253 | 58× | 119 | 27× |
| D:\Downloads | 762 | 5.24 | 290 | 55× | 125 | 24× |
| C:\Windows\SysWOW64 | 3,120 | 5.66 | 823 | 145× | 474 | 84× |
| C:\Windows\System32 | 5,029 | 6.42 | 1,328 | 207× | 739 | 115× |
| C:\Windows\WinSxS | 26,254 | 25.2 | 14,815 | 589× | 3,780 | 150× |

Explorer++ 的默认视图是图标视图，所以主结果用它；MyFiles 测的是详细信息视图（表格），和 Explorer++ 的详细信息视图那一列更对等。无论比哪一列，最小的差距是 16 倍。

MyFiles 的时间里，读目录加排序占大头的只有大文件夹（WinSxS 的 25 ms 里有 21 ms 是读 26,254 个目录项），其余是构建可见行、排版、绘制、交给显示，合计 2–3 ms，和文件夹大小无关。

Explorer++ 的时间随条目数线性增长（56 项 53 ms，5,029 项 1.3 s），因为它在界面线程上逐项向 shell 要信息，再逐项插进非虚拟的 ListView。详见 [design.md](design.md#和-explorer-的差别)。

### 第一次进和再次进

上表是改「第一帧就是最终的画面」（[design.md](design.md#第一帧就是最终的画面)）之前测的，详细信息视图。改完之后，文件夹读完还要为第一屏里系统没给过的图标和类型名最多等 10 ms，所以一次运行里第一次进某个文件夹会慢一些，再进不受影响。同一天稍后在中图标视图下重测（同样 5 轮，第 1 轮是第一次进，磁盘上的类型缓存是空的）：

| 文件夹 | 第 1 轮 | 第 2–5 轮中位 |
|---|---:|---:|
| D:\Code | 6.2 | 3.7 |
| C:\Users\fish | 4.5 | 4.6 |
| C:\Program Files | 5.1 | 4.9 |
| C:\Windows | 15.7 | 4.7 |
| C:\Windows\System32\drivers | 9.4 | 7.6 |
| C:\Windows\Fonts | 22.7 | 7.8 |
| D:\Downloads | 34.5 | 7.8 |
| C:\Windows\SysWOW64 | 11.0 | 8.8 |
| C:\Windows\System32 | 15.5 | 10.8 |
| C:\Windows\WinSxS | 39.7 | 39.3 |

第 1 轮多出来的不全是那 10 ms：新名字第一次排版、新图标第一次上传到显卡也在里面，这些原来就有。类型缓存是热的时候（紧接着的第二次运行），第 1 轮 Fonts 从 22.7 降到 7.8，Downloads 从 34.5 降到 18.2；C:\Windows 和 System32 仍是 16–21 ms（没有逐项查原因；exe 这类按路径取的图标不进这个缓存，每次运行都要重新取）。WinSxS 这次是 39 ms 而不是 25 ms，差在读目录上（这次 22–38 ms，上表那次 21 ms），构建只有 1 ms，和这次改动无关。

## 启动

从 `CreateProcess` 之前到第一个文件夹（`C:\`）的内容画出来，各 3 次：

| | 3 次 | 中位 |
|---|---|---:|
| MyFiles | 211 / 166 / 163 ms | 166 ms |
| Explorer++ | 335 / 330 / 326 ms | 330 ms |

MyFiles 的 166 ms 里，进程起来到开始建窗口约 10 ms，建窗口和第一次构建约 70 ms，第一帧的呈现（创建交换链、第一次提交）约 50–80 ms。Direct3D 设备在启动时就放到后台线程创建，否则还要再多 60 ms 左右。

已经在运行时再启动一次（比如别的程序让它打开一个文件夹），新进程把参数交给老进程就退出，约 5 ms。

## 滚动

在 System32（5,029 项）里连续滚动 80 次滚轮，详细信息和中图标视图各一半：每帧的构建、排版、绘制合计中位 1.6 ms，95 分位 2.6 ms，最大 3.4 ms。165 Hz 的一帧是 6 ms。

## 怎么测的

两边量的是同一件事：**从发出「去这个文件夹」到这个文件夹的内容画完**，都用 `QueryPerformanceCounter`，都不含 DWM 合成和等垂直同步的时间。

**MyFiles**（`myfiles --bench`，进程内）：起点是调用导航的那一刻，终点是包含新文件夹各行的那一帧交给显示之后（`Context.OnPresented`）。

**Explorer++**（`tools/expbench`，从外部）：

- 跑的是装好的 exe 的一份私有拷贝，放在 `%TEMP%\myfiles-expbench\`，带自己的 `config.xml`（便携模式），所以不读也不写你的 Explorer++ 设置。设置全是默认值，只关了它的导航提示音。
- 起点：路径已经填进地址栏的输入框，在向它发送 Enter（`WM_KEYDOWN`）之前读计数器。1.4.0 的地址栏在这个消息里同步完成整个导航。
- 终点：Enter 返回后，等当前标签页的 ListView 没有待绘区域，且它的线程应答了一个空消息，这时读计数器。也就是它自己画完，没有从外部催它重绘。
- 每次导航后核对：窗口标题是新文件夹，ListView 的条数和文件夹里的一致。

## 哪些地方不完全对等

对 Explorer++ 不利的：

- 从地址栏触发比在列表里双击多一段解析路径的工作。没有单独量，上限是 56 项文件夹的整个同步部分，42 ms。
- 它每 30 秒在界面线程自动保存一次设置，可能落进某一次测量；取中位数基本吸收掉了。
- 从外部测量多两次跨进程消息往返，合计不到 0.5 ms。

对 Explorer++ 有利的：

- 终点时它只画了名字和占位图标，真实图标、详细信息视图里名字以外的列都是之后补上的。MyFiles 的终点时日期、类型、大小列已经在画面里；图标在测上表时两边都是之后补，现在 MyFiles 见过的类型和 10 ms 内取到的图标也在终点的画面里。
- 它走 shell 枚举，在两个文件夹里条目更少：`C:\Users\fish` 是 156 对 166，`C:\Windows\Fonts` 是 268 个字体族对 598 个文件。

两边一样的：

- 每个文件夹访问 5 次取中位，都是热缓存。
- 两个进程都按前台程序的速度调度。Windows 会给不在前台的窗口所属进程降速，Explorer++ 的测试窗口不抢前台，所以工具对它关掉了这个降速，否则它的数字还要再慢到 1.5 倍。

三次完整运行之间，Explorer++ 小文件夹的中位数相差 5% 以内，大文件夹 10–20%；MyFiles 小文件夹在 3–6 ms 之间浮动。

## 重跑

```powershell
cd D:\Code\cairn
.\build.ps1
go build -o bin\expbench.exe .\tools\expbench\

bin\myfiles.exe --bench tools\bench-folders.txt --bench-out out\myfiles.json --bench-rounds 5
bin\expbench.exe -out out\explorerpp.json                       # 约 2 分钟，会开一个 Explorer++ 窗口
bin\expbench.exe -view details -out out\explorerpp-details.json
bin\expbench.exe -myfiles-startup bin\myfiles.exe C:\ -out out\myfiles-startup.json
bin\expbench.exe -compare out\myfiles.json out\explorerpp.json -startup out\myfiles-startup.json -md out\compare.md
```

要测的文件夹列在 `tools\bench-folders.txt`，一行一个，可以换成你自己常去的。`MYFILES_TRACE=1` 让 MyFiles 把每次导航各阶段的耗时打到标准错误上。

日常使用时，每个窗格状态栏右边的数字就是这个文件夹从请求到显示用的时间。
