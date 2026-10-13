# mobterm-cli 工程约定

## 范围与边界

- 先读目标代码及相关文档；坚持最小完整改动，复用现有实现。避免过度设计，不借任务重构、扩展功能或改动无关依赖、配置和用户数据。
- 保持短生命周期通知 CLI：不引入 daemon、监听端口、终端正文采集或 API 驱动的远程执行。
- Hooks 兼容范围与事件语义见 `docs/compatibility.md`；新增支持须验证实际版本和事件结构。保留用户既有 notifier、无关设置和后续编辑；未知配置不得覆盖。
- 配对秘密仅经有界 stdin 帧传入；保持 `store.go` 的私有权限、锁和原子写入。目标校验失败不得猜测 pane 或创建终端；不得记录或提交凭据、密钥、Hook 正文及构建产物。

## 代码与验证

- 根目录为单个 Go `main` 包：`main.go` 分派命令，`protocol.go` / `api.go` 处理协议与请求，`events.go` / `hooks.go` 处理事件与配置，`target.go` 校验目标，`store.go` 管理状态，`upgrade.go` 管理升级。
- 工具链以 `go.mod` 和 `.github/workflows/ci.yml` 为准，当前为 Go 1.27.1；保留 `go.sum`。本地构建：`go build -trimpath -o mobterm-cli .`。
- Go 改动按 CI 在仓库根目录执行以下检查；CI 在 Linux/macOS 上运行。纯文档改动核对事实、引用及 `git diff --check`。

  ```sh
  test -z "$(gofmt -l .)"
  go test ./...
  go vet ./...
  go test -race ./...
  ```

- 打包或升级相关改动另运行 `python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"`，核验 Linux/macOS × amd64/arm64 四平台产物；输出在 `dist/`。发布由 `.github/workflows/release.yml` 的稳定版本 tag 触发，普通提交推送不等于授权发版。
- 测试使用隔离临时配置；缺少真实 tmux 等依赖时的 skip 不算互操作通过。按改动更新相关文档，验证范围见 `docs/validation.md`。

## 跨仓库协议（App / CLI / API）

- 三仓库为 `../mobterm`（App）、`../mobterm-cli`、`../mobterm-api`；公开契约以 `../mobterm-cli/docs/protocol-v1.md` 为准。
- 修改 SSH stdin 帧/CLI JSON、REST 鉴权与状态（含 `token_generation`）、事件信封/AAD、目标身份或 APNs 字段时，检查三端生产者和消费者及新旧版本混用。破坏兼容时明确版本支持范围和升级顺序，不得仅改单仓导致配对、通知、撤销或跳转失效。
- 同步受影响的实现、文档与回归；加密契约须核对 CLI `testdata/protocol-v1.json`、API `tests/fixtures/protocol-v1.json` 和 App `Tests/Core/AgentNotificationTests.swift` 内嵌向量，并验证受影响的通知及跳转测试。遵守各仓库验证入口；缺少仓库或环境时明确未验证项，不以单仓测试代替跨端兼容性结论。
- 区分 API 接受、APNs 接受、手机显示和实际跳转；本地测试不能证明真实推送送达。
