# WorkBuddy WebUI 重构（已交付结构）

本文只记录本次已落地的前端结构、接口约束与验证证据，不是后续路线图。

## 宿主适配

管理页运行在 CPA 同源 iframe 内，可用宽度为主体视口减去约 210px 侧栏（折叠 54px），外层头部约 50px，移动端 ≤768px 侧栏转为覆盖层。因此本次不引入内层竖向 rail，改用横向任务导航：桌面单行，窄屏自动换行，390px 下为 3 列两行且全部可见。

正文 15px（手机 16px），次要文字 13/14px，手机控件最小 44px；内容最大宽度 1280px，宿主 1440 宽度下实际内容约 1180px。未使用渐变、装饰性卡片墙或通用 KPI 横幅。

## 静态结构与发布

| 文件 | 职责 |
|------|------|
| `web/index.html` | 页面骨架、六个任务视图、账号/登录原生 dialog |
| `web/style.css` | 主题 token 与全部布局、响应式、明暗对比 |
| `web/app.js` | 路由、数据加载、账号编辑、OAuth、主题与状态同步 |
| `web/theme.js` | 首屏前从 `workbuddy.ui.theme` 应用主题 |
| `web.go` | 枚举嵌入的 `web/` 顶层 `.html/.css/.js`，登记 MIME |
| `management_dispatch.go` | 按精确资源名发布，附 `Cache-Control: no-store`；未知、嵌套与 `..` 一律 404 |

资源地址沿用宿主约定 `/v0/resource/plugins/workbuddy/<name>`；`/index.html` 保留旧路径。页面内相对引用 `./theme.js`、`./style.css`、`./app.js`，因此每个源文件都实际被服务并被浏览器加载。

## 任务视图

| 导航 | 现有能力 |
|------|----------|
| 概览 | 账号/签到/流水聚合、需要关注、最近 Credits 变动；`/accounts`、`/credits-ledger`、`/last-checkin`、`/streak` |
| 账号 | 搜索、列表摘要、优先级预设菜单、登录/添加；账号编辑 dialog 保存备注与优先级、删除、显式确认 |
| 额度与签到 | 配额单选/全部（串行）、手动签到、权益查询与领取；三个下拉刷新后保留选择 |
| 模型 | 搜索；`workbuddy/<internal_id>` 调用 ID；同一内部 ID 合并全球/国内倍率，同展示名不同 ID 分行 |
| 记录 | 任务时间线、本地签到记录、Credits 流水（时间/账号/余额/变化/来源，完整 `auth_id` 在行 `data-auth-id` 与 title）与模型成本 |
| 设置 | 管理密钥、主题、数据范围说明 |

## 保留的契约与不变量

- 端点与字段语义不变：`/workbuddy/accounts|models|quota|checkin|benefits|benefits/claim|tasks|last-checkin|credits-ledger|streak`，以及宿主的 `GET /workbuddy-auth-url`、`GET /get-auth-status`、`PATCH /auth-files/fields`、`DELETE /auth-files?name=`。
- 凭证键仍为 `wb_mgmt_key`，密钥输入回显与保存流程未改；密钥不进入 URL、日志或页面文本。
- 备注优先显示（`note || nickname`），登录方式仍按原始 nickname 推断；global 的「上次签到」仍显示 `—`。
- 禁用/受限账号的备注只读且不发送 `note` 写入，避免覆盖插件维护的受限说明；优先级仍可保存。列表接口未回传 `disabled` 时，按备注中的固定 `WorkBuddy 11140` 受限标记识别并保护。
- 账号保存拆成优先级、备注两条 PATCH；失败时提示已成功字段与失败字段，对话框保持打开。
- 删除与三项批量优先级预设都要求 `confirm()`；视图切换不触发登录、签到或领取。
- 配额/账号/权益选择只按 `auth_index` 保留；每个面板有独立请求序号，切换选择、改密钥或新一轮请求都会使旧响应失效。未查询前不渲染额度数字。
- 请求身份按发起时的密钥判定：密钥已变更时，旧请求的 401/成功不再改写 `keyValid`，避免旧 401 把新密钥标记为无效。
- 单账号配额查询以 `{auth_index, epoch}` 记录在途请求，同一账号重复点击只发一次；切换账号仍可立即发起新查询并作废旧响应。
- 账号编辑器每次打开生成独立生命周期身份（`{authIndex, id}`），保存/删除完成只对该身份生效；期间关闭并重开同一账号或其它账号时，旧写入不回写错误、不关闭新面板，删除按钮状态只由当前面板决定。保存期间表单仍可编辑：若完成时检测到与提交值不同的新草稿，面板保持打开并保留该草稿，只提示已完成写入。
- 概览把流水/签到/连登的读取失败如实显示为「读取失败」与 `—`，同步状态改为「部分数据读取失败」，流水面板显示可重试的失败态，不把失败折算为 0 条或空态。
- OAuth：仅接受 http/https，先展示手动链接；轮询 2.5s、上限 5 分钟，可取消；关闭面板/改密钥会丢弃迟到的启动或轮询结果；轮询在 await 后核对发起时的 attempt，取消后旧轮询不得完成新登录；成功后关闭面板并刷新账号与模型。
- 主题只使用 `workbuddy.ui.theme`，由 `theme.js` 在 CSS 前应用；没有 host postMessage 协议。

## 验证

执行命令（仓库根目录，Windows PowerShell）：

```powershell
$env:CGO_ENABLED = '0'
go test ./... -shuffle=on -count=1
go vet ./...
go build ./...

node --check web/app.js
node --check web/theme.js
node --check uitest/ui.test.mjs

$env:PLAYWRIGHT_MODULE = 'C:/Users/admin/AppData/Roaming/npm/node_modules/@playwright/cli/node_modules/playwright'
$env:CHROMIUM_PATH = 'C:/Users/admin/AppData/Local/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-win64/chrome-headless-shell.exe'
node uitest/ui.test.mjs
```

结果：`go test ./... -shuffle=on -count=1` 四个包全部通过；`go vet`、`go build` 干净；浏览器套件 31/31 通过，控制台无 pageerror。

浏览器套件只连 `127.0.0.1` 上的 Node mock，覆盖：六视图导航与宿主 1230 宽度、手机 390 与 768 导航/字号/44px、账号列表语义、编辑取消/双字段保存/部分失败、禁用与受限标记备注保护、删除确认、预设确认与 global=10/cn=0、选择保留与迟到账号/配额响应、全部账号配额汇总、签到/权益领取显式写路径、总览 CN 签到口径与关注列表、加载失败重试与空态、改密钥丢弃旧响应并关闭对话、OAuth 取消/成功/启动中关闭/非法 URL/5 分钟上限、模型合并与过滤、流水全字段与成本、密钥协议与主题持久化、切换视图无自动写操作。本轮新增五条回归：旧密钥迟到 401 不改写新密钥状态（`page.route` 扣住旧请求，新密钥就绪后再放行 401）、取消后旧轮询不得完成新登录、旧保存/旧删除完成不得关闭后开的编辑面板、流水/签到/连登失败显示读取失败而非零、单账号配额重复点击只发一次查询且完成后可再次查询。编辑器生命周期补充三条回归（同一 `page.route` 扣住 PATCH/DELETE，放行前用真实点击重开面板）：同账号旧保存完成不得关闭重开的编辑面板（重开后回填与提交值相同，仅生命周期身份能判定）、同账号旧删除完成不得关闭重开的编辑面板、保存期间新输入的新草稿在提交完成后仍保留且服务端只落盘点击提交时的值。

变更前证据：同一套件的布局用例在旧页面上失败（缺少 `#tasknav`），记录见 `uitest/evidence/pre-implementation-failure.txt`。截图见 `uitest/evidence/`：桌面 1230 明暗账号页、390 明暗账号页、额度/模型/记录/设置、桌面与手机账号编辑面板。

## 边界

- 未对真实上游执行签到、权益领取、OAuth 登录或账号增删；全部写路径仅由 mock 验证。
- 已在现网宿主 HTML 的本地副本内检查浅深主题、展开/折叠侧栏与手机 iframe；管理请求全部使用合成数据。
- 发布版本为 `v0.1.55`；CI、生产验收与回滚记录由 VPS 运维手册维护。
- 待领权益改为在“额度与签到”视图显式查询，进入账号列表不会自动查询权益。
