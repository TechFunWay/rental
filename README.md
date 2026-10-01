# 租房管理（rental）

[![Release](https://img.shields.io/github/v/release/TechFunWay/rental)](https://github.com/TechFunWay/rental/releases/latest)
[![License](https://img.shields.io/github/license/TechFunWay/rental)](LICENSE)
[![Docker Image](https://img.shields.io/docker/v/techfunways/rental?sort=semver&label=docker%20image)](https://hub.docker.com/r/techfunways/rental)

面向个人房东、小型公寓运营者、工厂宿舍管理员的租房管理系统：结构化房源、租约到期提醒、月度抄表账单（水电气费用自动计算）、收款与欠缴跟踪、CSV 模板导入导出、收费单据打印；电脑端表格与手机端卡片双布局，支持飞牛 fnOS 一键登录与 Docker 自托管。

> A self-hosted rental management app for landlords, small apartment operators and dorm managers: rooms, tenants, monthly meter readings, bills, payments and receipts — with desktop tables and a mobile card layout. Docker / fnOS NAS ready.

版本功能与修复记录见 [CHANGELOG.md](./CHANGELOG.md)。最新安装包见 [Releases](https://github.com/TechFunWay/rental/releases)。

基于 [smallgo](https://gitee.com/TechFunWay/smallgo) 应用框架实例化（Go/Gin/GORM/SQLite + Vue 3/TS/Vite/Pinia/Tailwind），默认端口 **8910**。

如果这个项目对你有帮助，欢迎[支持作者](#支持作者)。

## 下载与安装

| 渠道 | 获取方式 |
|---|---|
| GitHub Releases | <https://github.com/TechFunWay/rental/releases> —— 各平台压缩包、飞牛 `fpk` 安装包与 `docker-compose.yml` |
| Gitee 发行版 | <https://gitee.com/TechFunWay/rental/releases> —— 国内镜像，产物与 GitHub 一致 |
| Docker 镜像 | `docker pull techfunways/rental:latest`（amd64 / arm64 多平台） |
| 飞牛 fnOS | 在飞牛应用中心手动安装 Releases 里的 `.fpk` 安装包（amd64 / arm64） |
| 官网介绍页 | <https://techfunway.wycto.cn/fnapp/rental> |

> 默认端口 `8910`；数据默认是挂载目录下的 SQLite 单文件，备份即拷贝，恢复支持上传本地备份文件。

## 应用界面

| 总览 | 抄表账单 |
|---|---|
| ![总览](docs/screenshots/dashboard.png) | ![抄表账单](docs/screenshots/bills.png) |

| 收费单据 | 房源管理 |
|---|---|
| ![收费单据](docs/screenshots/receipt.png) | ![房源管理](docs/screenshots/rooms.png) |

| 租户管理 | 手机端（总览 / 账单卡片） |
|---|---|
| ![租户管理](docs/screenshots/tenants.png) | ![手机端总览](docs/screenshots/mobile-dashboard.png) ![手机端账单](docs/screenshots/mobile-bills.png) |

| 账单详情 | 统计分析 |
|---|---|
| ![账单详情](docs/screenshots/bill-detail.png) | ![统计分析](docs/screenshots/analytics.png) |

> 移动端专属布局：底部导航栏、账单/房源/租户卡片式列表、弹窗自适应窄屏；桌面端保持全字段表格。明暗双主题自适应。

## 功能一览

- **房源管理**：按小区/楼栋/单元/楼层/房号结构化录入；默认租金/卫生费/管理费、电/燃气单价覆盖、抄表底数（吨/度/方）；不同小区允许同房号
- **租户管理**：入住/退租，宿舍场景同房多名租户；租约到期时间与剩余天数着色提醒
- **计费与缴费按租户设置**：水费按吨/包月与金额、缴费周期（月付/季付）、缴费日、提前提醒天数都在租户上填写——每个租户可以不一样；租户没填的项跟随偏好设置里的全局默认，登记租户时表单按全局默认预填
- **合同存档**：每个租户可上传多份合同文件（拍照/扫描件/PDF，≤20MB/个），文件存应用数据目录、读取需登录且校验归属，支持在线预览与下载，退租后记录保留
- **抄表台账**：每房每月的水/电/燃气表读数独立记录，自动算上期差值用量；空置房、季付房的中间月份也能记，抄表开票时自动沉淀一条
- **月度抄表账单**：一键生成当月账单，自动衔接上月读数；水电气费用 = 用量 × 单价（分位四舍五入）；应付合计 = 租金 + 水电燃气 + 卫生费 + 管理费；支持直填读数
- **月付 / 季付**：按租户设置缴费周期，季付每 3 个月出一张账单（租金/卫生费/管理费/包月水费按 3 个月计，抄表一季度一次，账期与首张账单月对齐）
- **缴费提醒**：按租户设置缴费日（1-28 号）与提前提醒天数，总览卡片与导航角标自动汇总临期与逾期；每天 8 点可经钉钉群机器人 / 邮箱 / 短信 / QQ 机器人推送当日汇总（渠道在偏好设置里绑定，绑定目标加密存储）
- **收款与欠缴**：多次收款累加，是否欠缴 / 欠缴额自动判定，欠缴徽标
- **模板导入导出**：CSV 模板下载（Excel 中文无乱码）、账单/房源导出、导入自动建房建租户、同月账单更新
- **收费单据**：单据编号、出租方抬头、缴费周期、明细与合计，浏览器打印 / 另存 PDF
- **数字带单位**：读数（吨/度/方）与单价（元/吨、元/度、元/方、元/月）在列表、弹窗与单据上都带单位显示
- **框架能力**：登录认证、飞牛 NAS 一键登录、用户管理、系统配置、备份恢复、审计日志、版本检查、明暗主题、赞赏支持
- **自定义收费项目**：除租金/水/电/燃气/卫生/管理六项外可自建收费项目（宽带费、停车费等），每项可设固定金额与月付/季付周期、可停用
- **按项目周期出账**：读数类项目按月收，固定费项目按租户缴费周期到期才收；季付租户的中间月出「月账单」、锚点月出「季账单」，一键出账自动跳过不该收的月份
- **租户收费项目勾选**：登记租户时逐项勾选是否参与结算（如不收卫生费、燃气），被排除的项目在账单与单据中计 0；租户档案另含身份证号
- **收款流水与统计分析**：每次收款单独记录并按项目分摊，收款弹窗可按项目勾选；新增收款记录页与统计分析页（汇总卡、收入趋势、项目构成）
- **账单详情**：账单可查看逐项费用（计费说明、周期、金额、已收、欠缴）与收款流水分摊明细

## 技术栈

- 后端：Go + Gin + GORM + SQLite（WAL，`CGO_ENABLED=1`）
- 前端：Vue 3 + TypeScript + Pinia + Vue Router + Tailwind CSS + Vite
- 部署：Docker（多平台）、飞牛 fnOS `.fpk`、裸二进制

## 快速开始

### 源码运行

```bash
# 后端（默认 :8910）
cd server && go run .

# 前端开发（:3000，代理 /api 与 /uploads 到 :8910）
cd web && npm ci && npm run dev

# 生产构建（前端必须先于后端）
make build

# 运行
./rental -data-dir ./data -web-dir ./static/dist -port 8910
```

首次启动注册的第一个用户为管理员。业务数据存储于 `data/db/rental.db`（SQLite，WAL）。

### Docker

```bash
# 构建本地镜像
make build-docker          # 当前机器架构
make build-docker-multi    # linux/amd64 + linux/arm64 合并本地 OCI

# 或使用发行目录中的 compose
cd release/v0.3.0   # 或解压 Release 资产后的目录
docker compose up -d
```

浏览器访问 `http://localhost:8910`。

### 飞牛 fnOS

在 [Releases](https://github.com/TechFunWay/rental/releases) 下载对应架构的 `.fpk`：

- `techfunway-rental_v0.3.0_x86.fpk`（Intel / x86_64 NAS）
- `techfunway-rental_v0.3.0_arm64.fpk`（ARM64 NAS）

### 各平台压缩包

Releases 中同时提供 Windows / macOS / Linux 的二进制压缩包，解压后带上 `www/` 静态资源即可运行。

## 业务数据规则

| 规则 | 说明 |
|---|---|
| 费用计算 | 用量 = max(0, 本月读数 − 上月读数)；费用 = 用量 × 单价，四舍五入到分 |
| 计费与缴费来源 | 租户设置 → 偏好设置中的全局默认（水费按吨/包月与金额、缴费周期、缴费日、提醒天数）；房间生效值取该房在租租户的设置，同房多名租户时逐项取第一个显式设置的租户 |
| 单价优先级 | 账单行内单价 > 房间覆盖价（电/燃气）> 偏好设置中的全局默认价 |
| 读数衔接 | 生成新账单时“上月读数”自动取上一期账单的“本月读数”，无历史则取房间底数 |
| 欠缴判定 | 应付合计 − 已收 > 0 即欠缴；差额为欠缴额（半分容差） |
| 删除保护 | 存在账单的房源不可删除；账单删除需二次确认 |

## 构建 fnOS / Docker

```bash
make build-all          # 五平台压缩包 + 截图 + compose 到 release/v<版本>/
make fnpack             # 飞牛 .fpk（x86 + arm64），需 Docker 与 fnpack
make build-docker-multi # 本地多平台合并镜像 OCI 归档
```

## 与上游框架同步

框架层文件与 smallgo 基线保持一致，业务代码集中在 `server/rental/` 与 `web/src/views|api`。检查漂移：

```bash
./scripts/check-sync.sh .
```

## 支持作者

本应用免费、无广告，所有数据都保存在你自己的设备上。如果它对你有帮助，欢迎请作者喝杯咖啡——金额随意，1 元也是心意；不赞赏也不影响任何功能。

<p align="center">
  <img src="docs/donate-wechat.png" alt="微信赞赏码" width="260" />
</p>

支付后可在应用内「支持作者」弹窗中点击【已支持】发送一次匿名支持计数（仅设备统计信息，与微信账号和支付记录无任何关联）。

## 许可证

本项目以开源方式发布，详见 [LICENSE](LICENSE)。
