# Go quality and Foundation benchmarks

[Operations](../operations/operability-performance.md#validation-commands) owns supported environments and validation entry points. The single required `baseline` extends `scripts/validate.sh` with formatting, direct default Staticcheck and reachable-symbol govulncheck analysis. Existing vet, race, module verification, isolated consumer and real PostgreSQL/restore proof retain their owners and run once. Tools supplement code review and behavior tests.

## Tool acquisition and scope

| Tool | Reviewed pin | Official source |
| --- | --- | --- |
| Staticcheck | 2026.2.1, `honnef.co/go/tools v0.8.1` | [Release](https://github.com/dominikh/go-tools/releases/tag/2026.2.1), [usage](https://staticcheck.dev/docs/getting-started/) |
| govulncheck | `golang.org/x/vuln v1.8.0` | [Go vulnerability analysis](https://go.dev/doc/security/vuln/), [command](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) |

`scripts/setup-quality-tools.sh` owns exact module/archive and manifest checksums plus source commit links. It requires Go 1.27.1 and enabled checksum verification, checks both pinned sums before building, and installs into a new absolute task-owned directory. Go verifies transitive downloads through its checksum database. Installation uses versioned `go install`, changes no repository manifest, and emits compiled build identities. No global installation or binary cache is required.

`scripts/check-gofmt.sh` checks all tracked Go source, including the consumer, without rewriting it. Runtime validation invokes it on the supported toolchain. Core scope analyzes only the root module; full scope also analyzes the existing isolated pinned consumer after its cold dependency download. Unknown paths and runtime/dependency changes select full. Documentation-only changes acquire no Go tools and perform no security scan. Known Core benchmark additions/modifications select Core; deletion/type change retains full. Routing/tool changes validate failure guards before acquiring tools. Checkout credentials are not persisted, workflow permissions remain read-only, and tools receive no production secrets or repository write credentials.

To acquire the same tools for a focused local investigation, use an absolute directory that does not exist:

```bash
scripts/setup-quality-tools.sh /tmp/achrix-quality-investigation
/tmp/achrix-quality-investigation/bin/staticcheck ./...
/tmp/achrix-quality-investigation/bin/govulncheck -db https://vuln.go.dev -show version,verbose ./...
```

Run these from the module being investigated with `GOWORK=off`; the routine Core/full commands already set it. Use default Staticcheck checks; there is no meta-linter or style-rule configuration. Review the source and compatibility of an updated pin, verify its sums, and run both modules before adoption.

## Vulnerability findings

govulncheck queries the current official `https://vuln.go.dev` database in source/symbol mode and prints scan details. Scanner/database failures and reachable findings fail validation; an unavailable scan never becomes a pass. Evidence is time-dependent: record candidate, scanner version, database update time and actual output in the PR/Issue when relevant.

For a finding, inspect its advisory, affected module/version, fixed version and reported call path. Determine how the application reaches that symbol and whether its input/trust boundary can exercise the defect. Reachability is useful evidence, not proof of external exploitability; a clean scan covers known database reports and the selected build context, not every security defect or platform.

Prefer the narrow compatible dependency/toolchain fix, refresh sums, and rerun affected behavior plus vulnerability analysis. If a report appears incorrect or cannot yet be fixed, persist the advisory, affected path, reasoning and unresolved risk for the owner; do not suppress it or weaken the scanner automatically. [Security reporting](../../SECURITY.md) governs confidential reports.

## Benchmark semantics and comparison

`achrix_bench_test.go` exercises the next-development-minor Core authorization ABI 2 after successful startup:

| Benchmark | Measured work |
| --- | --- |
| `BenchmarkApplicationAuthorizeAllowed` | Successful authorization through minimal context-aware policy, including Core admission/cancellation/drain accounting |
| `BenchmarkApplicationReadyThreeModules` | Readiness traversal for three composed modules with capability dependencies and cheap context-aware local callbacks |

Setup/startup/shutdown and the caller's parent-deadline construction are outside timed work. Each operation uses a valid reused parent deadline; Core's per-call context/cancellation/accounting remains measured. Allocations are reported. Module callbacks perform no network/database work. Composition/startup benchmarks wait for a concrete startup/module-count regression need.

On Go 1.27.1, run a quick local observation:

```bash
GOWORK=off go test -run '^$' -bench '^BenchmarkApplication(AuthorizeAllowed|ReadyThreeModules)$' -benchmem -count=1 .
```

For a performance-sensitive comparison, use the same machine, Go version, CPU settings and benchmark definitions at both commits, with other workloads quiet. Record both SHAs and environment. Run at least ten repetitions for each candidate and compare distributions and allocation output with [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat), for example:

```bash
# Run at the before commit, then the after commit with identical options.
GOWORK=off go test -run '^$' -bench '^BenchmarkApplication(AuthorizeAllowed|ReadyThreeModules)$' -benchmem -benchtime=1s -count=10 -cpu=1 . > before.txt
GOWORK=off go test -run '^$' -bench '^BenchmarkApplication(AuthorizeAllowed|ReadyThreeModules)$' -benchmem -benchtime=1s -count=10 -cpu=1 . > after.txt
benchstat before.txt after.txt
```

Use one reviewed benchstat version for both inputs and record `go version -m` for it. A currently compatible optional installation is `GOBIN=/absolute/owned/bin go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260929162123-406019bb8b68`; it is not a CI dependency. Inspect effect size, confidence intervals, sample count and allocation changes before attributing a regression; use profiles when a measured difference needs explanation.

CI compiles these benchmarks with Core tests but imposes no hosted-runner timing threshold. These Foundation measurements establish no HTTP/database/media throughput, product latency budget or capacity promise; the first real product owns those workload budgets.
