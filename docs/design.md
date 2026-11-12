# MyFiles 的设计

MyFiles 是用 [MyGo](https://github.com/egoist/mygo) 原生界面写的文件管理器，目标是在 Windows 上替代资源管理器：进文件夹要快，界面要紧凑，标签页能拖成上下左右的分屏。

这篇说它由哪些部分组成、每部分为什么这样做，以及从 Explorer++ 学了什么、没照搬什么。

## 分层

```
cmd/myfiles            入口：解析命令行，--help / --set-default 在这里答完就退出
internal/cli         命令行：MyFiles 自己的选项 + explorer.exe 的参数
internal/app         界面：窗口、分屏、标签、地址栏、各种视图、快捷键（全平台同一份）
internal/workspace   分屏布局：一棵二叉树，叶子是放标签的窗格（纯数据，不知道界面）
internal/config      设置和上次的会话（JSON，写在用户配置目录）
internal/fsys        文件系统：读目录、排序、路径、盘符、常用文件夹
internal/shell       桌面环境：图标、类型名、打开、复制/移动/删除、系统右键菜单、注册为默认文件管理器
internal/clock       高精度计时（测量用）
third_party/mygo     MyGo，带少量补丁（见 patches/mygo.patch）
```

依赖只往下走：`app` 用 `workspace`、`config`、`fsys`、`shell`；后四个互不依赖界面。

### 平台边界

和操作系统打交道的代码只在两个包里，各自用文件名后缀分开：

| 包 | Windows | 其他系统 |
|---|---|---|
| `fsys` | `list_windows.go`（一次系统调用拿到整个目录）、`path_windows.go`、`sortkey_windows.go`、`volumes_windows.go` | `fsys_other.go`：标准库实现，能编译、能跑 |
| `shell` | `*_windows.go`：COM / Win32，全部用 `syscall`，没有 cgo | `shell_other.go`：返回 `ErrUnsupported` 的桩 |

`shell/shell.go` 是这个包对外的全部接口，注释就是规格。移植到 macOS 时要做的是：给 `shell` 写一组 `*_darwin.go`（NSWorkspace 取图标、NSFileManager / Finder 做文件操作、废纸篓），需要的话给 `fsys` 写 `list_darwin.go`（`getattrlistbulk`）；`app`、`workspace`、`cli`、`config` 不用动。`GOOS=darwin go build ./...` 现在就能过，CI 里可以一直保持。

`app` 里还有一层小接口 `desktop`（`desktop.go`）：剪贴板、打开文件、系统菜单、属性页都经过它。正式运行时它转给 MyGo 和 `shell`；测试里换成只记录的假实现，所以测试不会在桌面上弹任何东西。

## 进文件夹为什么快

一次导航从按键到出画面经过这几步，每步的做法：

1. **读目录**（`fsys.List`）。打开目录句柄，用 `GetFileInformationByHandleEx(FileFullDirectoryInfo)` 配 256 KB 缓冲区，一次调用拿回几千项的名字、大小、时间、属性。这些都在目录索引里，不打开任何文件。名字从 UTF‑16 转 UTF‑8 时一个缓冲区只分配一次。System32（5,029 项）约 1.6 ms，WinSxS（26,254 项）约 21 ms。
2. **排序**（`fsys.Sort`）。先给每个名字算一次排序键（`LCMapStringEx`，数字按数值、忽略大小写、按用户语言，结果和资源管理器的顺序一致），多核并行算；之后比较就是比字节串。排的是下标数组，条目本身不动。5,029 项约 0.5 ms。
3. **等结果**（`navigate`）。读目录在另一个 goroutine 里做，主线程最多等 40 ms。本地磁盘远用不了这么久，所以文件夹在触发导航的那一帧里就画出来，不会先画一帧旧内容。超过 40 ms（网络盘、休眠的硬盘）就不等了，结果回来再刷新，窗口不卡。
4. **构建界面**。表格和网格都只构建可见的行（MyGo 的虚拟列表），所以 26,000 项和 50 项的构建时间一样。每行的日期、大小只在第一次滚进视野时格式化一次。
5. **图标和类型名**（`app/icons.go`）。在 3 个工作线程上向系统取，后请求的先做（那是用户正在看的）。普通文件按扩展名缓存，取的时候不碰磁盘；只有 exe、lnk、ico 这类自带图标的才按路径取。

MyGo 的补丁里有两处是为这条路径加的：`Context.Rebuild`（一帧内状态变了再构建一遍，而不是等下一帧）和 `Context.OnPresented`（这一帧交给显示之后回调，用来计时）。

### 第一帧就是最终的画面

新文件夹的第一帧如果和下一帧不一样，眼睛看到的就是「闪一下」。2026-11-12 查到三处会这样，都改掉了；`TestFolderShowsAtOnce` 在六种视图里逐像素比较「导航后的第一帧」和「稳定后的画面」，除状态栏的耗时数字外必须相同，新标签也一样。

- **网格的列数**（最明显的一处）。MyFiles 每进一个文件夹换一个新的 `GridState`，而 MyGo 的网格在状态不知道宽度时按**窗口**的宽度算列数，下一帧才按真实宽度重排，于是列表、图标视图里每次进文件夹内容都先多排几列再跳回来。补丁：状态是新的就用元素上一帧的宽度，元素也是新的（新标签）就用最近一层上一帧在的祖先的宽度。
- **图标和类型名的占位**。原来第一帧画通用图标和按扩展名猜的英文类型名（`TXT File`），真的到了再换。现在：
  - 按扩展名的类型名和图标存在磁盘上（`app/iconstore.go`，`%LOCALAPPDATA%\MyFiles\types.json` 和 `icons-<像素>.bin`，`MYFILES_CACHE_DIR` 可以换位置），启动时读进来，见过的类型第一帧就是对的。第一次用到某个类型时仍在后台向系统核对一次，一样就什么都不动，不一样才替换并重写文件。只存 128 像素以内的尺寸，每个文件最多 4,000 个类型。
  - 系统的大图标不管要多大都是 256 像素。工作线程把它缩到视图实际显示的像素（`icons.fit`）再用：省内存，而且每次运行得到的像素相同，磁盘上的和系统刚给的才能比较。
  - 不知道的（没见过的类型、exe 之类自带的图标）：文件夹读完后，主线程为第一屏要用的那些最多等 10 ms（`icons.await`，`iconWait`），等到的直接进第一帧；没等到的照旧先画占位、到了再换。只有正在显示的标签才等。
  - 类型名在系统回答之前留空，不再猜：填上比换掉不显眼。
- **地址栏**。放不下时收进「…」的级数按地址栏上一帧的宽度算，原来这个宽度记在标签上，新标签第一帧没有，会先全画出来再收。现在取的是窗格里地址栏这个元素上一帧的宽度，和哪个标签无关。

没改的：缩略图（图片、视频、PDF）仍然是之后补上的，取一张可能要几十毫秒；应用启动后的第一帧、新分出来的窗格的第一帧，网格还是可能先按窗口宽度排一次。

### 和 Explorer++ 的差别

读 Explorer++ 源码（`ShellBrowser/`）得到的结论，和 MyFiles 的取舍：

| | Explorer++ | MyFiles |
|---|---|---|
| 枚举 | `IShellFolder::EnumObjects`，默认在界面线程上，逐项取 pidl | 直接读文件系统目录，一次调用一批，在后台线程 |
| 每项信息 | 每项 3 次 `GetDisplayNameOf` + `GetAttributesOf` + `SHGetDataFromIDList` | 枚举时已经带回来了 |
| 列表 | 非虚拟的 ListView，每项 `InsertItem` | 虚拟列表，只建可见行 |
| 排序 | 比较函数里每次克隆 pidl、`StrCmpLogicalW` | 预先算好排序键，比较字节 |
| 图标 | 按完整路径缓存，先进先出 | 按扩展名缓存，后进先出 |
| 变更通知 | 每条通知整表重排 | 去抖后整个文件夹重读一遍（读一遍只要 1 ms 左右） |

代价也在这里：MyFiles 只显示文件系统里的文件夹。回收站、控制面板这类不是目录的 shell 位置，MyFiles 自己不显示，命令行里遇到就交还给资源管理器。

沿用 Explorer++ 的：导航带代数（generation），旧导航的结果回来直接丢掉；来不及的图标先出占位再换；文件操作全部走 `IFileOperation`，进度、冲突询问、回收站、撤销都是系统的；「设为默认文件管理器」写的注册表位置。

## 分屏和标签

`workspace.Layout` 是一棵二叉树：内部节点是一次分割（左右或上下，带比例），叶子是窗格，窗格里是一排标签。操作只有几个：`AddTab`、`RemoveTab`、`MoveTab`（同窗格内就是换位置）、`Split(目标窗格, 标签, 上/下/左/右/中)`。窗格里最后一个标签走了，窗格自动合并回相邻的。整个结构能直接序列化成 JSON，会话恢复就是把它读回来。

界面上（`app/strip.go`、`app/view.go`）：

- 拖标签到另一个标签上：插到它前面或后面（看落在哪一半）。
- 拖到窗格内容区：按落点分五个区，四边各占 25% 表示在那一侧分屏，中间表示并入这个窗格；拖动时用半透明色块预览结果。
- 拖文件悬停在标签上 0.6 秒，标签自动切过来（方便往它的子文件夹里放）。
- 分割线可拖，双击回到一半。

设置也是一个标签页（`Tab.settings`，内容在 `app/dialogs.go` 的 `settingsPage`）：Ctrl+, 或右上角的按钮打开，一个窗口只有一个，再按就切过去；可以像别的标签一样拖动、分屏、关闭。它没有文件夹，所以不显示工具栏和状态栏，文件夹的快捷键对它不起作用；会话里它的路径记成 `::settings`。

## 地址栏

`app/address.go`。平时每一级路径是一个按钮，点了直接去那一级（并选中刚才所在的子文件夹）；中键点在新标签里打开；文件也可以直接拖到某一级上。放不下时前面的几级收进「…」菜单。

点按钮右边的空白、按 Ctrl+L / Alt+D / F4，地址栏变成输入框，整条路径选中，可以直接复制、粘贴、输入。输入框接受的东西和命令行一样（`cli.Locate`）：绝对路径、相对当前文件夹的路径、`%USERPROFILE%`、`~`、`shell:Downloads`、`file:///`；输入的是文件就进它的文件夹并选中它。

## 视图

每个标签有自己的视图，新标签沿用上次选的，会话里按标签保存。Ctrl+Shift+1…6、Ctrl+滚轮或工具栏最右的按钮切换：

| 视图 | 实现 | 图标 |
|---|---|---|
| 详细信息 | `ui.Table`：列可排序、调宽、换位置 | 16 |
| 列表 | `ui.GridView`：只有名字，多列 | 16 |
| 平铺 | 同上：图标旁边是名字、类型、大小 | 32 |
| 中 / 大 / 超大图标 | 同上：图标在上，名字在下，最多两行 | 48 / 96 / 192 |

图标按视图需要的像素取系统里「不小于它」的那一档再缩小，不放大。三个图标视图里，图片、视频、PDF 用内容的缩略图代替图标（`shell.Thumbnail`，走系统的缩略图缓存），在单独的两个工作线程上做，不挡图标；云端占位文件不取，免得触发下载。选择、光标、右键菜单、拖放、重命名在所有视图里是同一套状态（`Tab.sel`、`Tab.cursor`），切视图不丢选择。

## 文件的复制、剪切、拖放

- **剪贴板**用系统的格式：`CF_HDROP` 文件列表 + `Preferred DropEffect`（2 = 剪切，5 = 复制）。所以在 MyFiles 里剪切的文件能粘贴到资源管理器，反过来也一样。剪切的文件图标变淡，粘贴一次后失效；剪贴板被别的内容顶掉时也恢复。
- **拖放**是系统级的（OLE）：行可以拖到别的窗格、别的 MyFiles 窗口、别的程序；别的程序拖进来的文件同样接收。落点可以是文件夹那一行、窗格空白处、标签、地址栏的某一级、侧栏的位置。
- **移动还是复制**和资源管理器一样：同一个卷默认移动，跨卷默认复制；按住 Ctrl 强制复制，Shift 强制移动。
- 真正搬文件的是 `shell.Copy / Move / Delete`，也就是系统的 `IFileOperation`：进度窗口、重名询问、回收站、Ctrl+Z 撤销记录都是系统的。同目录复制自动改名为「xxx - 副本」。

## 没有声音

- 启动时 `CoInternetSetFeatureEnabled(FEATURE_DISABLE_NAVIGATION_SOUNDS)` 关掉导航的「咔哒」声。
- 窗口没有系统菜单栏，Alt+字母、F10 这类按键不再进系统的菜单循环（那是「叮」的来源）；这是给 MyGo 打的补丁。
- 右键菜单、确认对话框、设置都画在窗口里，不用系统的消息框。
- 打开文件失败（比如文件没了）不弹系统对话框，错误显示在状态栏。
- 代码里没有任何 `MessageBeep` / `PlaySound`。

系统自己的界面仍然是系统的：文件操作的冲突询问、「更多选项…」打开的系统右键菜单、属性页。

### 打开方式

右键菜单的「Open With」是子菜单（`shell/openwith_windows.go`）：`SHAssocEnumHandlers` 按扩展名列出系统推荐的程序，带图标，点了用 `IAssocHandler::Invoke` 打开。列表在菜单打开时在后台取，按扩展名缓存 30 秒。图标的位置由程序自己报：图标文件加序号的用 `SHDefExtractIcon`，商店应用报的是包里的资源名，用 `SHLoadIndirectString` 换成 PNG 文件再读。

最后一项「Choose Another App…」启动系统的 `OpenWith.exe <文件>`。2026-11-12 在 Windows 11 26300 上试过的其他办法都不行：`ShellExecuteEx` 的 `openas` 动作 0.1 秒后返回成功但什么都不显示；`SHOpenWithDialog` 返回 0x80073D54（进程没有包标识）。`OpenWith.exe` 的窗口不在前台时会自己关掉（从后台脚本启动，1.5 秒时还在，2.5 秒时已退出），所以启动后对它调用 `AllowSetForegroundWindow`。这一项只在自动化驱动的窗口里试过，那种情况下 MyFiles 不在前台，对话框留不住；真实点击下是否留住还没有确认。

### More 子菜单：自己画系统的右键菜单

右键菜单的「More」里是系统菜单的各项（`app/sysmenu.go`、`shell/menuitems_windows.go`）。做法：

- 右键菜单一打开，就在后台让系统生成它的菜单（`shell.LoadMenu`，不显示），读出每项的文字、图标、是否禁用、子菜单；到了再补进 More，之前显示「Loading…」。
- 系统的菜单对象必须在生成它的线程上用，所以每个菜单占一个宿主线程，直到关闭（`takeHost` / `giveHost`）。
- 子菜单是扩展在展开时才填的：第一次要某个子菜单时，替它发 `WM_INITMENUPOPUP` 再读。
- 去掉 MyFiles 自己已有的项（按命令名：open、openas、cut、copy、paste、delete、rename、properties、refresh）和多余的分隔线。
- 扩展自己画的项（owner-draw）不给文字，跳过；它们还能从最后一项「System Menu…」打开的原生菜单里用到。
- 同一批文件的菜单在 0.6 秒内再次打开会沿用上一次的；执行过命令后作废。
- 启动 1 秒后在后台对一个文件、一个文件夹、一个文件夹的空白处各生成一次并丢弃（`warmSystemMenu`），把扩展预先装进进程。

### 系统菜单为什么慢

时间花在系统生成菜单上（`IContextMenu::QueryContextMenu`，要加载所有注册的右键扩展）。2026-11-12 实测，同一个进程里：第一次给文件生成 1.0–1.1 秒，第一次给文件夹生成 0.4–0.6 秒，之后每种类型 0.08–0.14 秒（png 第一次 0.23 秒）。先生成过文件夹的，再给各种文件生成都在 0.09–0.12 秒。也就是慢的是进程里头一次加载扩展，和具体文件类型关系不大，所以启动后预热一次就够。

## 默认文件管理器

设置里两个开关，对应 `shell.SetDefaultFileManager`，都只写当前用户（HKCU），不需要管理员权限：

- **用 MyFiles 打开文件夹**：`HKCU\Software\Classes\Directory\shell` 和 `Drive\shell` 下加一个 `myfiles` 动词并设为默认。别的程序用系统默认方式打开一个文件夹、桌面上双击文件夹、`start D:\dir` 都会到 MyFiles。直接点名资源管理器的调用不走这个关联，仍然是资源管理器：`explorer.exe /select,…`，以及「在文件夹中显示并选中」用的 `SHOpenFolderAndSelectItems`。
- **Win+E 打开 MyFiles**：`HKCU\Software\Classes\CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}\shell\opennewwindow\command`，带一个空的 `DelegateExecute`。

打开开关前会把原来的值记到 `HKCU\Software\MyFiles\DefaultBackup`，关掉时原样写回（包括「原来没有这个值」），所以如果之前把 Explorer++ 设成了默认，关掉 MyFiles 的开关后还是 Explorer++。命令行里 `myfiles --set-default` / `--unset-default` 做同样的事。

显式运行 `explorer.exe D:\dir` 仍然是资源管理器：那是直接启动它的程序，没有走「打开文件夹」的关联。

## 命令行

`myfiles --help` 有完整说明。两类参数都认：

```
myfiles D:\Code C:\Windows              每个路径一个标签
myfiles --split right D:\a D:\b         第二个起放到右边的窗格
myfiles --select D:\a\file.txt          打开 D:\a 并选中 file.txt
myfiles -n D:\Code                      新窗口

myfiles /select,D:\a\file.txt           explorer.exe 的写法
myfiles /e,/root,D:\Code
myfiles /n,D:\Code
myfiles shell:Downloads
myfiles ::{20D04FE0-3AEA-1069-A2D8-08002B30309D}     此电脑
```

MyFiles 是单实例的：第二次启动把参数交给已经在运行的那个（5 ms 左右返回），在最近用过的窗口里开标签。

## 测试

- `internal/app` 的测试用 MyGo 的 `ui.Tester` 在没有窗口的情况下跑整个界面：真的文件夹（临时目录）、真的文件操作，点击、按键、拖动都是合成的输入事件。覆盖导航、地址栏、过滤排序、拖标签分屏、复制剪切粘贴、拖文件、删除重命名新建、右键菜单、各视图，以及新文件夹的第一帧和稳定后的画面逐像素相同（`Tester.Image`）。
- `internal/shell` 的测试不弹任何界面；注册表测试只写 `HKCU\Software\MyFilesTest` 下的沙箱。
- 构建时加 `-tags e2e` 会带上 MyGo 的自动化插件，可以用 `mygo-automation` 从外面驱动真实窗口（读元素树、点击、截图）。`--ephemeral` 让这个实例不读也不写设置和会话。
- `myfiles --bench` 和 `tools/expbench` 见 [benchmark.md](benchmark.md)。
- `tools/deskcheck` 做无窗口测试做不了的三件事，都在真实桌面上：
  - `drag`：用真实鼠标把一行拖到另一处，走系统的拖放循环。只在起点所在的窗口里松手，指针离开了就按 Esc 放弃，不会把文件放到别处。
  - `keys`：真实按键（Alt+字母、F10 这些会让没有菜单栏的窗口「叮」的键）。窗口不在前台就不按。
  - `listen`：读默认输出设备上各音频会话的峰值表，报告这段时间里系统提示音会话和指定进程有没有出声。`-control -system` 会放一段比满幅低 90 dB、听不见的音，用来确认表是通的。

### 2026-11-12 在真实窗口里验证过的

| 项目 | 做法 | 结果 |
|---|---|---|
| 文件拖到另一个窗格 | `deskcheck drag`，真实鼠标 | 文件移过去了，两边列表都更新 |
| 文件拖到文件夹那一行 | 同上 | 文件进了那个文件夹 |
| 文件拖到同一窗格另一个标签的标签头上 | 同上 | 文件进了那个标签的文件夹 |
| 设置里的两个「默认文件管理器」开关 | 在真实窗口里点开设置、点两个复选框，再点掉 | 打开后注册表指向本程序，关掉后与之前逐行相同 |
| 没有提示音 | `deskcheck listen` 期间真实按下 Alt+Q / Z / X、F10、无匹配的字母、退到顶之后继续按 Backspace、空剪贴板 Ctrl+V，加上导航、右键菜单、打不开的地址 | 系统提示音会话峰值 0.000000；对照音在同一会话里读到 0.000038 |
| Win+E | 注册后注入真实的 Win+E | 打开的是 MyFiles，没有资源管理器窗口；还原后注册表与之前逐行相同 |
| 剪贴板互通 | 在 MyFiles 里复制 / 剪切后用 .NET 读系统剪贴板；写入资源管理器格式的剪切再在 MyFiles 里粘贴 | `FileDrop` + `Preferred DropEffect` 为 5 / 2；粘贴成移动 |
| 拖标签分屏、命令行交给已运行实例、会话恢复、各视图 | `mygo-automation` 驱动真实窗口 | 见各节 |

第三方 MyGo 不在仓库里：`tools/setup-mygo.ps1` 按固定的提交取下来并打补丁。

## MyGo 的补丁

`third_party/mygo` 基于上游 `d40e0b7`，改动在 `patches/mygo.patch`：

| 改动 | 用途 |
|---|---|
| `Context.Rebuild`、`Context.OnPresented` | 同一帧内出新文件夹；计时 |
| `ListState.DragData / DragOptions / RowBuilt / Edit` | 表格的行作为系统级拖放源；行作为放置目标；新建后直接进入重命名 |
| `GridState.DragData / DragOptions / ItemBuilt / Gap / Border` | 网格视图同上，外加紧凑的间距 |
| `GridView`：新状态用元素（或祖先）上一帧的宽度算列数 | 进文件夹的第一帧列数就是对的，见[第一帧就是最终的画面](#第一帧就是最终的画面) |
| `transfer.Drop.Suggested` | 知道拖放时用户有没有按 Ctrl / Shift，才能做「同卷移动、跨卷复制」 |
| `ui.Prewarm` | 启动时在后台线程创建 Direct3D 设备（加载显卡驱动要几十毫秒） |
| Windows 后端：没有菜单栏的窗口不进系统菜单循环 | 去掉 Alt+字母、F10 的提示音 |
| `Tester.DragData / DragDataOver / DropData / WheelWith` | 无窗口测试里模拟系统级拖放、带修饰键的滚轮 |

上游的 `ui`、`transfer`、`internal/windows` 测试在打了补丁后全部通过。
