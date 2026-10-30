# AChrix

**AChrix** (pronounced `ATCH-riks`; Persian: `اَچ‌ریکس`) is a reusable Go application foundation from [AChWorks](https://github.com/AChWorks). Build a product with a small Core and the optional Modules it needs, while keeping your product's behavior, data and deployment under your control.

Use AChrix when you want shared application capabilities that you can consume and upgrade as ordinary versioned dependencies. Core and official components have independent versions; you choose a compatible set and keep your product source in its own repository.

## What you can use

| Component | What it provides |
| --- | --- |
| Core | Application composition, authorization and startup/shutdown lifecycle |
| [Identity](identity/README.md) and [Audit](audit/README.md) | Accounts, passwords, sessions and accountable operations |
| [Media](media/README.md) | Private attachments with an authorized HTTP/MCP workflow and clean PNG/JPEG preparation |
| [Multi-Site](multisite/README.md) | Site resolution, authorized inventory with optional HTTP/MCP reads, and per-site Module selection planning |
| [Admin](admin/README.md) | A composable administration shell with Identity and Media screens, including file-based English/Persian localization and RTL/LTR presentation |
| [Installation](installation/README.md) | Installation requirement assessment and safe re-entry planning |
| [Bridge](bridge/README.md) | Optional MCP and JSON HTTP resource-server adapters for existing application operations and user permissions; products supply credential verification and connection admission |

Components expose deliberate Go contracts and selected web/MCP adapters. HTTP API and MCP coverage for relevant internal and external consumers is part of [capability development](docs/architecture/contracts-and-interfaces.md#internal-and-external-programmatic-coverage); current coverage is documented by each component and its examples. This does not yet mean every component operation has a network endpoint or MCP binding.

Supported failure paths preserve useful machine categories and corrective guidance. Runtime components provide structured local diagnostics, and products can compose a protected callback for the underlying operational cause; see [diagnosis and audit](docs/operations/operability-performance.md#diagnosis-and-audit) for access, correlation and retention responsibilities.

The first supported stack is Go with ordinary PostgreSQL for the components that need persistence. See the [supported environment](docs/operations/operability-performance.md#supported-environment) before installing.

## Get started

Create your product's Go module and choose an exact reviewed Core version from [Releases](https://github.com/AChWorks/achrix/releases):

```bash
mkdir my-product
cd my-product
go mod init example.com/my-product
ACHRIX_CORE_VERSION="<reviewed-core-version>"
go get "github.com/AChWorks/achrix@$ACHRIX_CORE_VERSION"
```

Follow the [product quick start](docs/architecture/consumption-and-packaging.md#start-a-product) for a runnable application, compatible Module selection and explicit database setup. The [Notes example](fixtures/notes/README.md) shows a separately composed consumer.

AChrix is currently a **pre-v1 developer foundation**. Review each component's release notes before adoption. Existing consumers should follow the [upgrade guide](docs/lifecycle/lifecycle-and-compatibility.md#monolithic-to-independent-module-packaging), especially when moving from a shared-version release to independent components. Products own their permission policy, actual installation/activation and deployment.

## Learn more and contribute

- [Documentation](docs/PROJECT-MAP.md): architecture, component contracts and operating guides.
- [Issues](https://github.com/AChWorks/achrix/issues): bugs, requests and planned work.
- [Contributing](CONTRIBUTING.md): development and contribution requirements.
- [MPL-2.0 license](LICENSE), [licensing guide](docs/legal/licensing.md) and [trademark policy](TRADEMARKS.md).
