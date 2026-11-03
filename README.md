# CPAMP Viewer

[![CI](https://github.com/jinshenganyuci/cpamp-viewer/actions/workflows/ci.yml/badge.svg)](https://github.com/jinshenganyuci/cpamp-viewer/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**给朋友看账号统计和额度的只读面板。** 默认连接 CPA Manager Plus；可额外接入多个 CPA Manager Plus 和 Sub2API，合并各 CPAMP 的监控、统计、账号与 Key 额度；Sub2API 补充账号额度。管理密钥留在服务端，浏览器没有修改账号或重置额度的权限。

当前应用版本 **2.8.0**，已适配 CPAMP **v1.14.1** 的相关只读功能。镜像提供 `linux/amd64`：

```text
jinshenganyuci/cpamp-viewer:2.8.0
jinshenganyuci/cpamp-viewer:latest
```

`latest` 是滚动标签；需要固定行为时使用版本号。离线 Docker 包和 Windows 程序见 [Releases](https://github.com/jinshenganyuci/cpamp-viewer/releases)。

## 功能

| 页面 | 内容 |
| --- | --- |
| 仪表盘 | 请求、Token、费用、模型与服务状态 |
| 用量分析 | 时间范围、模型、调用方、凭证统计；历史明细清理后的覆盖提示 |
| 请求监控 | 请求模型、路由模型、实际响应模型与“模型不一致”提示 |
| 配额管理 | 多个实例的账号额度；Codex 普通池 / Spark 分开显示；重置卡剩余次数和逐卡到期倒计时；其他供应商已保存快照 |
| Key 额度 | Access Guard 指定公开条目的名称、剩余额度与重置时间 |
| 用量状态 | 在线/已归档/已清理记录、存储占用和维护状态，仅查看 |

未知额度不会显示成满额或用尽。没有 API Key 的历史请求仍计入汇总，不会被错误用作 Key 筛选。

## Docker 快速开始

需要已运行的 **CPAMP Manager Server**、它的 **Admin Key**，以及 Docker Compose。Viewer 连接 CPAMP，不是把 CPA 推理地址或调用用的 API Key 填进来。

### 1. 获取项目

```bash
git clone https://github.com/jinshenganyuci/cpamp-viewer.git
cd cpamp-viewer
cp .env.example .env
mkdir -p secrets
```

### 2. 配置连接

编辑 `.env` 中以下内容。如果 CPAMP 已有可访问的 HTTPS 地址：

```dotenv
CPAMP_BASE_URL=https://你的CPAMP管理域名
CLIPROXY_NETWORK=bridge
CPAMP_VIEWER_VERSION=2.8.0
CPAMP_VIEWER_BIND=127.0.0.1
CPAMP_VIEWER_PORT=18417
```

如果 CPAMP 和 Viewer 在同一台机器的 Docker 中，建议共用 CPAMP 的 Docker 网络。查询网络名：

```bash
docker inspect cpa-manager-plus \
  --format '{{range $name, $config := .NetworkSettings.Networks}}{{$name}}{{println}}{{end}}'
```

把容器名换成你的实际 CPAMP 容器名；然后配置：

```dotenv
CPAMP_BASE_URL=http://cpa-manager-plus:18317
CLIPROXY_NETWORK=上面查询到的网络名
```

这里的 `127.0.0.1` 在容器里指向容器自身，不能用它代替另一台机器或另一个容器的地址。

### 3. 写入密钥

Linux / Bash 下用隐藏输入写入 **CPAMP Admin Key**，避免把密钥写在命令历史中：

```bash
umask 077
read -rsp 'CPAMP Admin Key: ' viewer_admin_input; echo
printf '%s' "$viewer_admin_input" > secrets/cpamp_admin_key.txt
unset viewer_admin_input
openssl rand -base64 48 > secrets/viewer_session_secret.txt
sudo chown root:65532 secrets/*.txt
sudo chmod 0640 secrets/*.txt
```

第二个文件用于会话/分页游标，不是访客登录密码。容器以 UID/GID `65532` 运行，因此文件需要让该组可读。不要将 `secrets/` 或 `.env` 提交到 Git。

### 4. 启动

```bash
docker compose pull
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:18417/health
```

打开 **http://127.0.0.1:18417/management.html**。

默认是公开只读模式，拿到访问地址即可查看，无需登录密码。通过域名提供服务时，把反向代理指向 `http://127.0.0.1:18417`。需要直接通过服务器 IP 访问时，将 `CPAMP_VIEWER_BIND=0.0.0.0` 后重建容器，并按需要限制防火墙或代理访问范围。

## 接入多个 CPA / Sub2API

**`jinshenganyuci/cpamp-viewer:2.8.0` 和本次发布的 `latest` 已包含多来源功能，直接拉取即可。** 旧 `2.6.0` 镜像只支持主 CPAMP 和一个 Sub2API 来源。保留 `CPAMP_BASE_URL` 和对应管理密钥作为主 CPA；额外的 CPA、CPA Manager Plus 和 Sub2API 实例通过 `VIEWER_QUOTA_SOURCES`（JSON 数组）或 `VIEWER_QUOTA_SOURCES_FILE`（JSON 文件）配置，二选一。

从 2.8.0 起，配置的所有 CPAMP 自动参与仪表盘、用量分析、请求监控、账号和调用密钥筛选、配额管理、公开 Key 额度及用量状态。已在 2.7.0 配好 `VIEWER_QUOTA_SOURCES` 的部署直接升级即可，无需换参数或新增文件。Sub2API 继续只补充账号额度，`/health` 继续检查主 CPAMP。包含主 CPAMP 和现有单 Sub2API 在内，最多接入 16 个来源。

同名账号和 Key 带实例名称，保持独立身份、筛选和缓存；一个实例失败时保留健康实例并显示不完整提示。请求列表按时间合并，翻页保留各实例进度；如果历史数据在翻页期间被修改，需刷新后继续。合并后的 P95、缺少有效样本数的平均延迟和活跃日均值显示未知；不同实例的模型价格不互相套用。

### 现有 Compose 和 .env：追加来源

保留原 Compose 的端口、密钥文件、主 CPAMP、Sub2API 和 Access Guard 配置，仅确认镜像并在现有 `environment` 下追加一项：

```yaml
services:
  cpamp-viewer:
    image: jinshenganyuci/cpamp-viewer:latest
    environment:
      VIEWER_QUOTA_SOURCES: ${VIEWER_QUOTA_SOURCES:-[]}
```

这是需合入原文件的片段。在原 `.env` 中追加一行，填写第二个 CPAMP 的管理地址和 Admin Key：

```dotenv
VIEWER_QUOTA_SOURCES='[{"type":"cpamp","name":"CPAMP 二号","base_url":"https://cpamp-two.example.com","admin_key":"替换为二号CPAMP的AdminKey"}]'
```

只在数组中填写新增实例，**不要重复填写原 `CPAMP_BASE_URL` 或已有 `SUB2API_BASE_URL` 来源**。新增 Sub2API 使用 `"type":"sub2api"` 和 `"admin_api_key":"替换为Sub2API管理员APIKey"`。`name` 是所有访客可见的实例名称；需要多个新增实例时，在同一个 JSON 数组内追加对象。

```bash
docker compose pull cpamp-viewer
docker compose up -d --no-deps cpamp-viewer
```

`.env` 变量必须经过上面的 `environment` 映射才会传入容器。JSON 保持为单行，并用外层单引号包住，避免 Compose 对密钥中的 `$` 插值；不要提交真实 `.env`。这种方式不增加文件或脚本，也不要同时设置 `VIEWER_QUOTA_SOURCES_FILE`。管理员凭据保留在服务端容器环境变量中。

### 仓库自带 Compose：私密 JSON 文件

文件格式见 [`deploy/quota-sources.example.json`](deploy/quota-sources.example.json)。`type` 为 `cpa`（也接受 `cpamp`）时填写 `admin_key`，为 `sub2api` 时填写 `admin_api_key` 或 `admin_jwt` 中的一项。所有凭据字段也支持相应的 `_file` 字段，例如 `admin_key_file`，文件路径必须能在 Viewer 进程中读取。`name` 不填写时自动编号；地址和管理员密钥不会返回浏览器。套餐保留原始值。

保留原 `.env`，先将其中 `CPAMP_VIEWER_VERSION` 设为 `2.8.0` 或 `latest`。将示例复制到 `./secrets/quota_sources.json`，填写真实来源后，让容器用户（UID/GID 65532）可读取该文件：

```bash
cp deploy/quota-sources.example.json secrets/quota_sources.json
# 编辑 secrets/quota_sources.json，填写新增来源。
sudo chown root:65532 secrets/quota_sources.json
sudo chmod 0640 secrets/quota_sources.json
docker compose -f compose.yaml -f compose.quota-sources.yaml pull cpamp-viewer
docker compose -f compose.yaml -f compose.quota-sources.yaml up -d --no-build --no-deps cpamp-viewer
```

文件方案不要同时设置内联 `VIEWER_QUOTA_SOURCES`；之后的拉取、重建和停止也保留这个叠加文件。如果继续使用原有 Sub2API 文件方案，同时保留 `-f compose.sub2api.yaml`；把该来源迁入列表时则移除其旧 `SUB2API_*` 配置，避免重复。通过 `_file` 引用其他密钥文件时，还需自行挂载那些文件。原生运行时可直接设置 `VIEWER_QUOTA_SOURCES_FILE=/path/to/quota_sources.json`。

如果原有 Sub2API 地址已经更换为 CPA，将该条目的 `type` 改为 `cpa`，使用 CPA Management Key，并移除该地址旧的 Sub2API 配置。

## 同时接入 Sub2API

连接 CPA Manager Plus 的同时，可额外读取 Sub2API 管理端的账号列表、Anthropic OAuth/Setup Token 的**被动用量快照**、OpenAI Codex 账号列表中已保存的 5H/7D 或其他明确时长的窗口，以及已配置的 API Key 账号额度。在现有“配额管理”页合并展示，并通过卡片上的“CPA Manager Plus / Sub2API”标识区分来源；其他页面继续显示 CPA Manager Plus 数据。Sub2API 账号优先显示已有的邮箱和套餐，两种来源的套餐均保留原始值。OpenAI 账号不请求仅支持 Anthropic 的 `source=passive` 接口；没有有效的已保存窗口时显示“暂无已记录的额度快照”，不会推测不存在的 5H 窗口，也不会主动探测上游、刷新凭据或修改账号。Sub2API 账号数量上限为 2000，超过时页面显示该来源不可用提示，不会静默遗漏；任一来源不可用时仍尽量展示另一来源。

**单个 Sub2API 来源自 2.6.0 起已包含在正式镜像中，2.8.0 继续兼容，无需本地构建。** Sub2API 是可选的第二个额度来源，不能替代 CPAMP：仍须保留有效的 `CPAMP_BASE_URL` 和 CPAMP Admin Key。两个管理地址都必须从 Viewer 容器可达。

### 现有自定义 Compose：只加两项环境变量

不想新增密钥文件时，在原服务的 `image` 和 `environment` 中修改以下字段，其余配置保留：

```yaml
services:
  cpamp-viewer:
    image: jinshenganyuci/cpamp-viewer:2.8.0
    environment:
      SUB2API_BASE_URL: ${SUB2API_BASE_URL}
      SUB2API_ADMIN_API_KEY: ${SUB2API_ADMIN_API_KEY}
```

在原 `.env` 增加：

```dotenv
SUB2API_BASE_URL=https://sub2api.example.com
SUB2API_ADMIN_API_KEY=替换为Sub2API管理员APIKey
```

然后在原部署目录运行 `docker compose pull cpamp-viewer` 和 `docker compose up -d --no-deps cpamp-viewer`。这里需要 **管理员 API Key，不是模型调用 Key**。`.env` 只供 Compose 插值，必须同时添加上面的 `environment` 映射；只写 `.env` 不会生效。密钥仍留在服务端，但会存在容器环境变量中；不要把 `.env` 提交到 Git。此方式不要叠加下一节的文件密钥方案，也不要同时设置 `SUB2API_ADMIN_API_KEY_FILE`。

### 仓库自带 Compose：独立密钥文件

已部署的用户保留原 `.env`，将版本改为 `2.8.0`，再新增 `SUB2API_BASE_URL` 和 `SUB2API_ADMIN_API_KEY_PATH`。下面是一份可用的 `.env` 示例；按实际情况替换管理地址，原有 Access Guard 公开设置继续保留：

```dotenv
CPAMP_VIEWER_VERSION=2.8.0
VIEWER_PUBLIC_ACCESS=true
TZ=Asia/Shanghai
CPAMP_VIEWER_BIND=127.0.0.1
CPAMP_VIEWER_PORT=18417
CLIPROXY_NETWORK=bridge
CPAMP_BASE_URL=https://cpamp.example.com
CPAMP_ADMIN_KEY_PATH=./secrets/cpamp_admin_key.txt
VIEWER_SESSION_SECRET_PATH=./secrets/viewer_session_secret.txt
VIEWER_SECURE_COOKIES=false
VIEWER_SESSION_TTL=12h
VIEWER_MAX_ANALYTICS_RANGE=8784h
VIEWER_MAX_EVENTS_PAGE=200
SUB2API_BASE_URL=https://sub2api.example.com
SUB2API_ADMIN_API_KEY_PATH=./secrets/sub2api_admin_api_key.txt
```

若通过 Docker 容器名连接上游，将 `CLIPROXY_NETWORK` 改为能访问两个服务的现有自定义网络，例如 `shared_backend`；相应地址可填写 `http://cpa-manager-plus:18317` 和 `http://sub2api:8080`。Docker 自带的 `bridge` 不提供这些容器名的 DNS 解析；跨主机连接使用 HTTPS。

保留原 CPAMP Admin Key 文件；另将 **Sub2API 管理员 API Key** 写入 `SUB2API_ADMIN_API_KEY_PATH` 指向的文件，不能使用普通调用 API Key。Compose 使用 `compose.sub2api.yaml` 叠加挂载第二份密钥：

```bash
mkdir -p secrets
umask 077
read -rsp 'Sub2API Admin API Key: ' sub2api_admin_input; echo
printf '%s' "$sub2api_admin_input" > secrets/sub2api_admin_api_key.txt
unset sub2api_admin_input
sudo chown root:65532 secrets/sub2api_admin_api_key.txt
sudo chmod 0640 secrets/sub2api_admin_api_key.txt
docker compose -f compose.yaml -f compose.sub2api.yaml pull cpamp-viewer
docker compose -f compose.yaml -f compose.sub2api.yaml up -d --no-build --no-deps cpamp-viewer
curl -fsS http://127.0.0.1:18417/health
```

**使用本节叠加文件方案时，以后每次拉取、升级、重建、查看日志或停止服务，都保留这两个 `-f` 参数。** 只执行 `docker compose up -d` 会遗漏 Sub2API 的环境变量和密钥挂载。仅修改 `.env` 不会自动把新增变量传进容器，必须使用叠加文件。

访问 `/management.html#/quota`。原生运行可设置 `SUB2API_BASE_URL` 和 `SUB2API_ADMIN_API_KEY`（或 `SUB2API_ADMIN_API_KEY_FILE`）；不用 API Key 时可改用 `SUB2API_ADMIN_JWT`。不设置 `SUB2API_BASE_URL` 时仅使用 CPAMP，原有配置与页面行为不变。

## 公开 Access Guard 的 Key 额度

默认不公开任何 Key。插件需安装在 CPA，CPAMP 需支持转发插件读取接口。

指定公开条目：在 `.env` 中填写绑定 ID 和展示名，**不填写 API Key**：

```dotenv
ACCESS_GUARD_PUBLIC_KEYS='{"keys":[{"binding_id":"native-key-example","name":"示例用户"}]}'
```

如果希望公开所有当前及以后新增的绑定，将上面的名单配置删除，改为：

```dotenv
ACCESS_GUARD_PUBLIC_ALL=true
```

两种方式互斥。应用配置：

```bash
docker compose up -d --no-deps --force-recreate cpamp-viewer
```

通过叠加文件启用 Sub2API 时，上面的重建命令改用：

```bash
docker compose -f compose.yaml -f compose.sub2api.yaml up -d --no-build --no-deps --force-recreate cpamp-viewer
```

访问 `/management.html#/key-quota`。正常情况下复用 `CPAMP_BASE_URL` 和 CPAMP Admin Key，**不需要再配置 `ACCESS_GUARD_BASE_URL`**；独立连接 CPA 的兼容方式见 [高级部署说明](docs/DEPLOYMENT.md)。

### 多 CPAMP 的 Key 额度和重置卡

`ACCESS_GUARD_PUBLIC_ALL=true` 会公开所有已配置 CPAMP 的绑定名称和额度，每张卡标注实例。现有全公开部署无需新增参数，也无需设置 `ACCESS_GUARD_BASE_URL`。主实例的明确公开名单不会套用到副实例。需要按实例控制时，可在对应 `VIEWER_QUOTA_SOURCES` 对象添加以下任意一项：

```json
{"type":"cpamp","name":"CPAMP 二号","base_url":"https://cpamp-two.example.com","admin_key":"替换为管理密钥","access_guard_public_all":false}
```

`access_guard_public_all` 为 `true` 时公开该实例所有绑定，为 `false` 时关闭；或使用 `"access_guard_public_keys":[{"binding_id":"native-key-example","name":"公开名称"}]` 指定该实例名单。两项互斥；空名单保持不公开。

Codex 凭证卡显示重置卡剩余次数、每张卡的剩余时间和到期日期。只读取上游已存在的卡，不提供消费或重置操作。上游只提供数量时保留数量并说明到期详情不完整；查询失败或没有此能力时显示未知，不当作 0 次。

## 升级与日常操作

升级前在 `.env` 设置目标 `CPAMP_VIEWER_VERSION`，或选择 `latest`：

```bash
docker compose pull cpamp-viewer
docker compose up -d --no-deps cpamp-viewer
```

通过叠加文件启用 Sub2API 的部署使用：

```bash
docker compose -f compose.yaml -f compose.sub2api.yaml pull cpamp-viewer
docker compose -f compose.yaml -f compose.sub2api.yaml up -d --no-build --no-deps cpamp-viewer
docker compose -f compose.yaml -f compose.sub2api.yaml logs --tail 100 cpamp-viewer
# 停止时同样使用这两个文件：
# docker compose -f compose.yaml -f compose.sub2api.yaml down
```

仅连接 CPAMP 或使用原有自定义 Compose 时，日常命令为：

```bash
docker compose logs --tail 100 cpamp-viewer  # 查看日志
docker compose down                        # 停止本项目 Viewer
```

Viewer 不保存 CPAMP 请求历史；重建 Viewer 不会清除 CPAMP 数据。本项目通过 `network_mode` 加入指定网络，`down` 不会创建或删除 CPAMP 网络。

## 常用配置

| 配置 | 用途 / 默认值 |
| --- | --- |
| `CPAMP_BASE_URL` | CPAMP 管理地址，必须从 Viewer 容器内可达 |
| `SUB2API_BASE_URL` | 可选的 Sub2API 管理地址，配置后与 CPAMP 并行读取额度 |
| `VIEWER_QUOTA_SOURCES` | 额外实例 JSON；CPAMP 合并各只读页面，Sub2API 补充额度；需 Compose 映射，与 `_FILE` 二选一 |
| `VIEWER_QUOTA_SOURCES_FILE` | 服务端读取的私密 JSON 文件；仓库使用 `compose.quota-sources.yaml` 挂载 |
| `VIEWER_QUOTA_SOURCES_PATH` | 文件方案在宿主机的路径，默认 `./secrets/quota_sources.json` |
| `SUB2API_ADMIN_API_KEY_PATH` | 叠加 Compose 文件中的 Sub2API **管理员** API Key 文件，默认 `./secrets/sub2api_admin_api_key.txt` |
| `CPAMP_ADMIN_KEY_PATH` | 管理密钥文件，默认 `./secrets/cpamp_admin_key.txt` |
| `VIEWER_SESSION_SECRET_PATH` | 内部签名密钥文件，默认 `./secrets/viewer_session_secret.txt` |
| `CLIPROXY_NETWORK` | 加入的现有 Docker 网络；远程 HTTPS 地址可用 `bridge` |
| `CPAMP_VIEWER_VERSION` | 发布镜像标签，默认 `2.8.0` |
| `CPAMP_VIEWER_BIND` / `CPAMP_VIEWER_PORT` | 宿主机绑定地址 / 端口，默认 `127.0.0.1:18417` |
| `ACCESS_GUARD_PUBLIC_ALL` | 是否公开全部绑定，默认 `false` |
| `ACCESS_GUARD_PUBLIC_KEYS` | 明确的公开名单，与上项互斥 |
| `VIEWER_MAX_ANALYTICS_RANGE` | 最大统计查询跨度，默认 `8784h` |
| `VIEWER_MAX_EVENTS_PAGE` | 请求明细每页上限，默认 `200` |

`.env` 是 Compose 的配置来源，Go 程序本身不会自动读取它。原生运行需要显式设置环境变量。其他参数、密码模式与离线/Windows 运行方式见 [高级部署说明](docs/DEPLOYMENT.md)。

## 一起开发

前端在 **`web-cpamp/`**，后端在 **`server/`**。本仓库不包含旧前端原型、真实配置、历史验收数据或编译产物。

```bash
git clone https://github.com/jinshenganyuci/cpamp-viewer.git
cd cpamp-viewer
git switch -c feat/你的功能名
```

接下来按照 [CONTRIBUTING.md](CONTRIBUTING.md) 启动本地 mock、Go 服务和支持热更新的 Vite；其中写明构建顺序、测试命令、分支和 PR 流程。架构与修改入口见 [架构说明](docs/ARCHITECTURE.md)。

- 有仓库写权限：推送自己的功能分支后提交 PR。
- 没有写权限：先 Fork 仓库，开发后提交 PR。
- CI 自动执行前端类型/样式检查、测试、构建，以及 Go 测试 / race / vet；不使用生产密钥，也不会自动推 Docker 镜像。
- 每次新增能力都必须保留服务端只读边界。参见 [SECURITY.md](SECURITY.md)。

完整发布构建（需要 Bash 和 Docker）：

```bash
./scripts/build-release.sh
```

输出到 `release/<版本号>/`，包括镜像、Windows 程序、部署包、许可证和 SHA-256 校验。此命令只构建本地产物，不自动发布。

## 常见问题

- **容器 unhealthy / `/health` 返回 503**：确认 CPAMP 地址、容器网络和 Admin Key，查看 Viewer 日志。
- **`/run/secrets/... permission denied`**：按上文设置密钥文件属组 `65532`、权限 `0640`。
- **配置了 Sub2API 却没有来源卡片**：自定义 Compose 检查 `environment` 映射，仓库文件方案确认启动时同时使用 `-f compose.yaml -f compose.sub2api.yaml`；检查管理员 API Key、容器网络和页面来源错误提示。没有已采样数据时不会显示满额。
- **Key 额度为空**：检查公开名单/开关及插件绑定；公开名单为空时不会自动展示。
- **新字段或用量状态不可用**：上游 CPAMP 必须提供对应接口和已采集数据。部分配额来自保存的观测快照；缺失数据不是满额。
- **`go test` 提示 `webdist` 没有文件**：先按开发指南构建前端，再从 `server/` 目录运行 Go 命令。

## 来源与许可证

Viewer UI 派生自 [seakee/CPA-Manager-Plus](https://github.com/seakee/CPA-Manager-Plus)，共享基线 v1.12.8，选择性适配 v1.14.1；并非原管理端的完整复制。保留上游 MIT 版权与许可证，详见 [UPSTREAM.md](web-cpamp/UPSTREAM.md) 和 [适配边界](docs/UPSTREAM_BASELINE.md)。

本项目采用 [MIT](LICENSE)。CPA Manager Plus、ECharts、zrender、React 等第三方部分保留各自许可证；发布镜像及离线包含 `licenses/`。费用是估算值，不等于提供方账单。
