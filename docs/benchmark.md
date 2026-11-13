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

测启动时不要开着同名的程序。2026-11-12 发现：已经有一个同名 exe 在运行时，再起一个独立实例（`--bench`、`--screenshot`、`--ephemeral` 这些不走单实例交接的模式），到第一帧要多花约 700 ms（约 280 ms 变成约 950–1,000 ms）；把 exe 换个名字，或者关掉正在运行的那个，就恢复。同一份 v0.1.0 的 exe 改名成 `MyFiles.exe` 也一样慢，所以和代码改动无关，原因没有查。正常使用碰不到：第二次启动只是把参数交给正在运行的实例。

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

## 搜索

2026-11-13，Ryzen AI 9 HX 370（24 线程）、NVMe、Windows 11 26300。索引的设计见 [design.md](design.md#搜索)。

### 读盘和建索引

`tools/idxbench` 读一个盘、建索引、存盘，再跑查询：

```powershell
go build -o out\idxbench.exe .\tools\idxbench
out\idxbench.exe -root D:\ -save out\idx report rdme      # 逐个文件夹读（不需要管理员）
out\idxbench.exe -root D:\ -mft -save out\idx report      # 读 MFT（在管理员终端里跑）
out\idxbench.exe -root D:\ -save out\idx -load -limit 1000 report   # 映射已存的索引，不读盘
out\idxbench.exe -root D:\ -save out\idx -load -rest report           # 每次搜索前把索引的页交还系统：停手之后第一次搜索的耗时
out\idxbench.exe -root D:\ -paths -save out\idx report                # 像没有文件编号的文件系统那样按路径读
```

每一步之后它打印进程的内存：私有的（专用工作集，任务管理器显示的那个数，5 毫秒采样一次的最高值）、提交的、整个工作集。

| 盘 | 文件数 | 读 MFT | 逐个文件夹 | 建索引（直接建在文件里，含刷盘） | 再次启动时映射 |
|---|---|---|---|---|---|
| D:（NTFS） | 236 万 | 1.35 s | 2.0–2.6 s | 0.67–0.76 s，179 MB | 0.5–1.1 ms |
| C:（NTFS，MFT 3.7 GB） | 277 万 | 5.3 s | 17–29 s | 1.1 s，219 MB | |
| G:（第三方驱动挂的 EXT4） | 约 400 万 | — | 约 3 分钟（2.5 万个/秒） | 305 MB | |
| F:（同上） | 约 750 万 | — | 约 4.5 分钟 | 574 MB | |

读 MFT 的数字是提权跑的（一次 UAC），而且是索引改成建在文件里之前测的；改了之后没有再提权跑过真盘（UAC 没有人点），这条路现在由合成卷的测试 `TestScanTable` 覆盖，它走的是同一套临时文件和建文件的代码。C 盘逐个文件夹读比 MFT 少 8.7 万个条目，是当前用户无权列出的文件夹里的。

内存，`idxbench` 读 D 盘并建索引的整个过程：私有内存最高 13 MB，工作集最高 108 MB（其中是临时文件和索引文件的映射）；映射之后按规则标记完，私有 12 MB。索引改成建在文件里之前，同一过程要几百 MB 的堆。程序本身在真实窗口里的内存见 [design.md](design.md#实测)。

按规则给 236 万行算标记（`go test ./internal/index -bench Mark`，`MYFILES_INDEX_FILE` 指向一个真的索引文件）：35 ms，分配 2.4 MB。

程序自己的提权读盘（设置里的按钮，`myfiles --index-helper`）也实际跑过一次：D:、E: 两个盘，退出码 0，写出的索引能映射、能搜。

### 查询

D 盘，236 万行，24 线程，各跑 60–100 次取中位数，包含取前 N 条并拼出路径：

| 查询 | 匹配数 | 取前 1000 条 | 取前 10000 条 |
|---|---|---|---|
| `report` | 15,481 | 4.6 ms | 10.8 ms |
| `rdme`（模糊） | 34,995 | 5.1 ms | 19.2 ms |
| `.pdf$` | 4,913 | 2.8 ms | 5.5 ms |
| `'report`（原样） | 1,867 | | 3.4 ms |
| `main go`（两个词，带上级文件夹匹配） | 21,173 | 12.0 ms | 16.8 ms |
| `src\main`（路径） | 16,096 | | 11.6 ms |
| `cq`（含拼音） | 18,893 | | 11.9 ms |
| `e`（一个字母，最坏情况） | 1,158,639 | 34.8 ms | 45.8 ms |

取前 10000 条时大半时间花在给结果拼路径和同分排序上（`report`：扫描 3.7 ms，出结果 7.2 ms）。界面一次取 2000 条。

上表是页都在内存里时的数字。10 秒没有搜索，索引的页会交还给系统；之后的第一次搜索要把页调回来（`-rest`）：`report` 12 ms（成批预取之前是 60 ms 上下），`e` 46 ms。

### 和 Lertaro 在同一台机器上比

Lertaro 自己的性能测试（`Tests/Core/IndexV2/Search/SearchPerformanceWorkloadTests.cs`）用的是合成数据：Z 盘上 100 个文件夹 `Archive000`…`Archive099`，6 万个文件 `report-000000.txt`…，每个文件夹 600 个，每第 10 个是 `.md`。它跑 6 个查询，各预热 3 次、测 10 次。把它的源码在这台机器上编出来跑（.NET SDK 10.0.401，Release，`DOTNET_TieredCompilation=0`）：

```powershell
$env:DOTNET_TieredCompilation = '0'
dotnet build D:\Code\_ref\lertaro-src\Tests\Core\Core.csproj -c Release --artifacts-path out\lertaro-artifacts
dotnet test  D:\Code\_ref\lertaro-src\Tests\Core\Core.csproj -c Release --no-build --artifacts-path out\lertaro-artifacts `
    --filter "TestCategory=SearchPerformance" --logger "console;verbosity=detailed"      # 输出里的 SEARCH_PERF 行
```

MyFiles 这边，`internal/index/workload_test.go` 造一模一样的数据、跑一模一样的查询、限制和筛选：

```powershell
go test ./internal/index -run xxx -bench LertaroWorkload -benchtime 1000x -count 3
```

2026-11-13，同一台机器（Ryzen AI 9 HX 370，24 线程），中位数：

| 查询（6 万个文件） | Lertaro | MyFiles |
|---|---|---|
| `report`，取 204 条，第一次 | 5.67 ms | 1.2 ms |
| `report`，取 204 条，同样的再搜一次 | 0.003 ms | 0.0001 ms |
| `report`，全部 6 万条 | 48.1 ms | 6.9 ms |
| `archive report`，全部 | 66.1 ms | 11.6 ms |
| `archive\report`，全部 | 76.7 ms | 7.2 ms |
| `archive\report`，取 204 条，只要 `*.txt`，第一次 | 10.0 ms | 1.3 ms |
| `z:\archive000\`，取 204 条，只要 `*.txt`，第一次 | 0.088 ms | 0.042 ms |

Lertaro 的测试对取 204 条的查询量到的是它的结果页缓存命中（0.003 ms），第一次的数字取自它的 `query-page-cache` 场景的 `coldMs`；带筛选的那一条它没有缓存命中，10.0 ms 是每次都算。MyFiles 的「第一次」是每次搜索前让缓存失效量出来的。

**这次对比改出来的东西。** 第一次照它的数据跑，MyFiles 在「全部 6 万条」上是 105 ms，比它的 48 ms 慢一倍多，取 204 条也要 4 ms。原因有三个，都改了：行数少时整个区间只切成两块，24 个核只用上两个（现在按行数切，每个核都有活干）；6 万条同分的结果按名称的自然顺序排，比较函数逐字符解码，而且排序搬的是 88 字节的结果本身（现在 ASCII 名称走字节比较，排的是下标，上万条时分块并行排再归并）；给结果拼路径是单线程的（现在分段并行）。之前拿来和它文档里的数字比的那组数据（15 个词轮换的名称，只有十五分之一匹配）恰好绕开了这几个问题。

仓库里原有的 `BenchmarkSearch`（`go test ./internal/index -bench Search`）用的是名称更多样的数据，对着的是 Lertaro 文档里它作者机器上（12 逻辑处理器的 Intel）的数字，不是同台对比：

| | Lertaro（它的机器，它的数据） | MyFiles，24 线程 | 限制成 12 线程 | 限制成 1 线程 |
|---|---|---|---|---|
| `report`，取 204 条 | 10.4–10.6 ms | 0.43 ms | 0.44 ms | 1.29 ms |
| `report`，全部 7432 条 | 94.6–101.8 ms | 3.3 ms | 3.5 ms | 4.2 ms |
| 带文件夹的路径查询，取 204 条 | 13.8–14.9 ms | 0.51 ms | 0.46 ms | 1.26 ms |

同一个基准放大到 600 万个文件：`report` 取 204 条 16.8 ms（单线程 133 ms）。这些都是每次搜索前让结果缓存失效量的。

### 跟进变化

`MYFILES_INDEX_LIVE_TEST=1 go test ./internal/index -run TestFollowDrive`（会读整个盘）：在真实的 D 盘上新建、改名、文件夹改名、文件夹移动、删除，每一步之后搜索结果都跟上；关掉管理器、在它背后改文件、再启动，从存盘的索引加日志追上来用了 17 ms，没有重读盘。`go test ./internal/app -run TestSearchFollowsTheDrive` 在界面里做同样的事：显示着的结果自己更新。

读整段日志（35 MB，30 万条记录）不提权 57 ms。
