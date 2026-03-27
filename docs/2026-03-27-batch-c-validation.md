# Batch C 验证记录（2026-03-27）

## 范围

本批次仅覆盖文档与说明对齐：

1. 将 `README.md` 中的 Go 版本说明与仓库实际要求对齐
2. 在 `README.md` 中补充本地构建/开发所依赖的工具链说明
3. 留下本次修正文档的简要验证记录，便于后续审查与收口

## 对齐结论

- `go.mod` 当前要求：`go 1.25.5`
- CI 当前通过 `actions/setup-go@v5` 的 `go-version-file: go.mod` 读取 Go 版本
- 前端构建/检查当前统一使用 Bun（见 `web/bun.lock`、`Makefile`、`.github/workflows/ci.yml`）
- `README.md` 已更新为以上口径，不再保留旧的 Go 版本徽标说明

## 验证记录

- PASS — `grep '^go ' go.mod` 结果为 `go 1.25.5`
- PASS — `rg -n 'go-version-file|bun run|bun install' .github/workflows/ci.yml Makefile` 能定位到 CI 与本地命令中的 Go/Bun 使用位置
- PASS — `git diff -- README.md docs/2026-03-27-batch-c-validation.md` 确认本批次仅修改 README 与本验证记录

## 备注

- 本记录对应 `docs/2026-03-27-current-open-issues.md` 中“README 里的 Go 版本说明和仓库实际要求还不一致”这一项的文档收口。
- 这里记录的是工具链说明对齐，不替代代码层面的实现/测试结论。
