# 高级部署与运行说明

以下命令默认从仓库根目录执行。首次安装建议先阅读根目录 [README](../README.md)。

# CPAMP Viewer

对应应用版本：`2.6.0`（默认公开只读，无登录密码）。

CPA Manager Plus 的独立只读观察台，只展示：

- 仪表盘：`/management.html#/`
- 配额管理：`/management.html#/quota`
- Key 额度：`/management.html#/key-quota`（Access Guard 公开名单）
- 用量分析：`/management.html#/usage-analytics`
- 请求监控：`/management.html#/monitoring`
- 用量状态：`/management.html#/usage-status`

支持使用 Viewer 匿名 ID 的调用方筛选：

```text
/management.html#/usage-analytics?api_key_id=view_<匿名ID>
```

原始 API Key 和 64 位 `api_key_hash` 不作为 URL、请求参数或响应字段进入浏览器。

## UI 来源

前端直接基于 [seakee/CPA-Manager-Plus](https://github.com/seakee/CPA-Manager-Plus) 原版 UI 源码裁剪：

- 共享 UI 基线：`v1.12.8`
- 本轮只读适配：`v1.14.1`（`aa8c5e9886b42ec82d78a419a1e1f59700ca90c5`）
- Commit：`7c4cbeadaa801613e98ea6874b902844f09e59c6`
- License：MIT，见 `web-cpamp/UPSTREAM_LICENSE`
- 来源说明：`web-cpamp/UPSTREAM.md`

Viewer 2.6.0 的共享界面以 CPAMP v1.12.8 为基础，并选择性适配 v1.14.1 的只读统计、来源识别、额度快照与用量状态；数据通过 Viewer 的只读 API 获取，左侧导航为六个页面。配额管理保留 Viewer 自有的脱敏展示逻辑。生产构建使用同源的 HTML + JS + CSS 三文件，以继续保持 `script-src 'self'`，不需要为内联脚本放宽 CSP。

## 2.6.0 新增 Sub2API 额度来源

正式 Docker 镜像现在包含 Sub2API 适配器。可在“配额管理”合并查看 CPA Manager Plus 和 Sub2API 账号，并保留来源标识；其他页面继续使用 CPAMP。修复 Spark 窗口误归入普通 Codex、未采样的 Anthropic 额度误显示为 100% 两处问题。接入方法见下文“同时接入 Sub2API”。

只连接 CPAMP 的现有用户不需要增加参数。Sub2API 是可选的第二个来源，不能替代必需的 CPAMP 连接。

## 已包含的 CPAMP 只读功能

- 对齐 CPAMP v1.14.1：Meta/Muse 与 Devin 的提供方识别、额度快照和 Meta 提供方数量统计。
- 非 Codex 配额使用 CPAMP 已保存的配额快照，保留多窗口、真实观测时间和过期标记；未知百分比显示未知，xAI 零金额月窗口不会被造出额度。
- 同步 Devin 缓存输入计算：缓存读取包含在 prompt 内，缓存创建单独计入；上游明确提供的总 Token 仍优先使用。
- 用量分析与请求监控显示历史明细清理后的覆盖范围提示，区分汇总统计和仍在线的请求明细，不把缺失明细当成没有历史请求。
- 新增 `/management.html#/usage-status` 只读用量状态页，显示在线/归档/已清理记录数、时间范围、存储占用及维护状态。没有归档、删除、导入、导出或配置操作。
- 保留请求/路由/响应模型与“模型不一致”提示、未定价模型提醒、独立 Codex/Spark 额度及 Access Guard 公开范围。

新字段需要 CPAMP 实际提供；旧版本缺少快照/用量状态接口时按能力降级，不会把失败当成零额度或满额。仅使用这些 CPAMP 功能时，不需要新增环境变量或密钥文件。

镜像同时发布 `jinshenganyuci/cpamp-viewer:2.6.0` 和 `jinshenganyuci/cpamp-viewer:latest`，发布时两个标签指向同一镜像；具体 digest 与校验文件见对应 GitHub Release。生产部署可固定版本号，或使用 latest 后主动拉取重建。

## 安全边界

Viewer 不挂载 `usage.sqlite`，不启动第二个采集器，也不提供通用 CPAMP 代理。

2.6.0 默认启用 `VIEWER_PUBLIC_ACCESS=true`：任何能够访问 Viewer 地址的人都可以直接查看六个只读页面，不需要密码，也不会获得会话 Cookie。公开模式下“退出登录”按钮不会显示。若域名不应对所有人开放，请在 Nginx Proxy Manager、CDN 或防火墙层限制访问者。

浏览器只访问：

```text
/viewer/api/v1/session
/viewer/api/v1/login
/viewer/api/v1/logout
/viewer/api/v1/dashboard
/viewer/api/v1/maintenance
/viewer/api/v1/aliases
/viewer/api/v1/quota
/viewer/api/v1/access-guard/quotas
/viewer/api/v1/model-prices
/viewer/api/v1/model-price-status
/viewer/api/v1/usage-status
/viewer/api/v1/analytics
```

以下原始管理路径不会被转发：

```text
/v0/management/*
/usage-service/*
/setup
/config.yaml
/auth-files/*
```

`CPAMP_ADMIN_KEY` 和可选的 Sub2API 管理员密钥只存在于 Viewer 服务端。公开模式不会向浏览器签发 Viewer 会话凭据，浏览器也不会获得 CPAMP 密钥、Sub2API 管理员密钥或 CPA Management Key。

Viewer 会从 CPAMP 只读加载 API Key 别名和当前统计维度，并在服务端按 `api_key_hash`、账号、来源、认证文件和项目 ID 完成关联。浏览器只会收到可见名称和 `view_*` 匿名 ID，不会收到完整 API Key、原始 64 位哈希、auth index、source hash 或 Header Trace。历史 Key 即使已没有当前别名，仍可按统计响应中的匿名 ID 筛选。

Viewer 已同步 CPAMP v1.12.8 的新品牌外观、数据库维护提示、“昨天”时间范围、空时间桶补齐、无请求延迟空值、调用方排名上下文、供应商范围账号身份及供应商感知套餐名称。缺失客户端 Key 的合成统计仍计入总量，但不会进入筛选器、趋势或下钻。

Viewer 不下发客户端 IP、`X-Forwarded-For`、User-Agent、Header Trace、原始账号 subject、认证正文和 response metadata；需要参与界面关联的身份均在服务端转换为 `view_*`。因此三个原版页面的视觉与非敏感统计保持一致，但这些敏感诊断字段不会出现在公开 Viewer。

使用 Docker 内部地址 `http://cpa-manager-plus:18317` 调用 CPAMP 时，Admin Key 会出现在该网络内的 HTTP 请求头中。生产环境应让 Viewer 与 CPAMP 位于专用的、非公开 Docker 网络，并限制宿主机及其他容器加入该网络；如果跨主机或经过不可信网络连接，必须把 `CPAMP_BASE_URL` 改为受信任证书保护的 HTTPS 地址。不要把 CPAMP 端口直接暴露给公网。

配额页的 Codex 额度优先通过既有 CPAMP 连接读取完整 `/wham/usage` 结果。Viewer 服务端只构造固定的 CPA `api-call` 封装：目标固定为 `https://chatgpt.com/backend-api/wham/usage`，实际 HTTP 方法固定为 GET，凭据索引和可选账号 ID 仅取自认证后的 auth-files 元数据。CPA 在服务端代入 Token；浏览器不会获得 Token、管理代理或自定义 URL / 方法 / Header 的入口。此查询不调用额度重置、积分消耗或任何配置修改接口。

Viewer 根据完整结果的 `rate_limit` 和 `additional_rate_limits` 分开显示普通 Codex 与 Spark，按真实窗口时长显示 5H / 7D。普通池仅有 7D 时不会添加 5H；完整结果替换全部旧快照，不再拼入重复的“额度池未确认”或零值空窗口。刷新共享 60 秒缓存，最多 4 个并发查询，失败也有冷却；更新失败保留最近成功结果并提示。无成功结果时才降级显示被动观测并说明查询失败。其它供应商通过固定 `POST /v0/management/quota-snapshots/query` 读取 CPAMP 已保存的快照（此接口只查询，目标由服务端认证文件元数据构造），不可由访客选择目标。接口不可用时回退请求响应头快照；不下载认证文件正文，不发起 Meta DCA 换 Key，不调用配额同步写接口。

完整读取不需要增加环境变量或密钥文件。查询时出现网络或账号认证错误不会被当成满额。

配额页允许展示 CPAMP 已记录的账号显示名，并在界面默认脱敏；API Key、管理密钥、认证正文、Token、Header Trace、原始哈希和上游分页 ID 不会下发到浏览器。

## 同时接入 Sub2API

使用 `jinshenganyuci/cpamp-viewer:2.6.0` 或本次发布的 `latest` 即可，无需从源码重新构建。现有自定义 Compose 也可直接在 `environment` 映射 `SUB2API_BASE_URL` 和 `SUB2API_ADMIN_API_KEY`，对应值写在 `.env`，然后按原方式拉取并重建；参见 [最少改动的示例](../README.md#现有自定义-compose只加两项环境变量)。不要同时配置直接密钥和文件密钥。

以下为仓库自带 Compose 的文件密钥方案。保留 CPAMP 地址和密钥，在已有 `.env` 中增加：

```dotenv
SUB2API_BASE_URL=https://sub2api.example.com
SUB2API_ADMIN_API_KEY_PATH=./secrets/sub2api_admin_api_key.txt
```

`SUB2API_BASE_URL` 是 **Sub2API 管理服务地址**，不是模型调用端点。密钥必须是 **Sub2API 管理员 API Key**，普通模型调用 API Key 不适用。若地址通过 Docker 容器名访问，Viewer 必须加入能访问两个上游的现有自定义网络；远程 HTTPS 地址可继续使用 `CLIPROXY_NETWORK=bridge`。包含原有 CPAMP 配置的完整示例见 [README 的 Sub2API 章节](../README.md#同时接入-sub2api)。

Linux / Bash 下创建单独的密钥文件：

```bash
mkdir -p secrets
umask 077
read -rsp 'Sub2API Admin API Key: ' sub2api_admin_input; echo
printf '%s' "$sub2api_admin_input" > secrets/sub2api_admin_api_key.txt
unset sub2api_admin_input
sudo chown root:65532 secrets/sub2api_admin_api_key.txt
sudo chmod 0640 secrets/sub2api_admin_api_key.txt
```

如果配置了不同的 `SUB2API_ADMIN_API_KEY_PATH`，写入和权限命令也使用该路径。基础 Compose 继续挂载原有 CPAMP 密钥和 Viewer 签名密钥；叠加文件新增 Sub2API 密钥，不替换其他密钥。

在线镜像启动或升级：

```bash
docker compose -f compose.yaml -f compose.sub2api.yaml pull cpamp-viewer
docker compose -f compose.yaml -f compose.sub2api.yaml up -d --no-build --no-deps cpamp-viewer
```

已用 `docker load` 加载离线镜像时，改用离线 Compose，并禁止构建：

```bash
docker compose -f docker-compose.viewer.yml -f compose.sub2api.yaml up -d --no-build --no-deps cpamp-viewer
```

**使用文件密钥方案时，以后每次重建、改配置、看日志和停止都保留 Sub2API 叠加文件。** 例如在线部署修改密钥后：

```bash
docker compose -f compose.yaml -f compose.sub2api.yaml up -d --no-build --no-deps --force-recreate cpamp-viewer
docker compose -f compose.yaml -f compose.sub2api.yaml logs --tail 100 cpamp-viewer
```

离线部署将上述基础文件换成 `docker-compose.viewer.yml`。不要在已启用 Sub2API 的项目中只运行基础 Compose，否则容器会缺少 Sub2API 配置。

Viewer 只读管理端的账号列表，以及 Anthropic OAuth/Setup Token 的被动用量。OpenAI Codex 使用列表中已保存的 5H/7D 窗口，不调用仅适用于 Anthropic 的被动接口，也不主动刷新上游凭据。没有已保存数据时显示暂无快照；缺少百分比保持未知。账号数量超过 2000 时该来源报告不可用，不静默截断；一个来源失败时，配额页尽量保留另一个来源的数据。`/health` 仍检查 Viewer 与 CPAMP，不能用健康检查成功替代 Sub2API 连接验收；在 `/management.html#/quota` 确认来源卡片和错误提示。

## Access Guard 的公开 Key 额度

Viewer 可直接复用现有 `CPAMP_BASE_URL` 和 `CPAMP_ADMIN_KEY` 读取 Access Guard。无需新增 CPA 管理密钥文件或手写名单文件。已用真实 CPAMP v1.12.8 验证：CPAMP 认证自己的 Admin Key 后，使用它保存的 CPA 管理密钥转发固定的插件读取请求，Viewer 不会读取这把 CPA 密钥。

### 自动显示全部绑定

在原有 Compose 服务的 `environment` 中增加一行即可：

```yaml
ACCESS_GUARD_PUBLIC_ALL: "true"
```

此模式公开插件里所有当前及未来新增绑定的名称和额度。访客只能看名字、金额和重置时间，没有管理操作。未设置名字或名字含密钥特征时使用通用的 `Key 1` 等名称；内部绑定 ID、Key 预览、凭证组和模型权限不下发。这里的美元额度来自插件账本，不代表上游账户余额或 Key 可调用状态。

使用项目提供的基础 Compose 时，也可在原 `.env` 追加 `ACCESS_GUARD_PUBLIC_ALL=true`，基础 Compose 已负责传入容器。自定义 Compose 必须在服务 `environment` 中添加该变量。没有设置此开关或公开名单时，新版仍默认不公开任何绑定，不会因换镜像而意外扩大范围。

### 指定公开条目，无需文件

不使用 `PUBLIC_ALL=true`，改为在 Compose 的 `environment` 中写内联名单：

```yaml
ACCESS_GUARD_PUBLIC_KEYS: '{"keys":[{"binding_id":"native-key-example","name":"示例用户"}]}'
```

名单只填写绑定 ID 和展示名，不填写 API Key。此模式不会自动公开其他绑定；空名单 `{"keys":[]}` 继续保持不公开。全部展示、内联名单和 `ACCESS_GUARD_PUBLIC_KEYS_FILE` 三种方式互斥，配置冲突会直接报错。

更新镜像或修改上述设置后，在原部署目录重建 Viewer：

```bash
docker compose up -d --no-build --no-deps --force-recreate cpamp-viewer
```

通过叠加文件启用 Sub2API 时，使用上一节包含 `-f compose.sub2api.yaml` 的重建命令。

页面地址是 `/management.html#/key-quota`。未启用公开范围显示“额度暂未开放”；已启用但没有条目显示“暂无公开额度”。页面可见时每 30 秒刷新，后端共享 15 秒缓存；读取失败不会把余额伪装为满额。

### 高级：保留独立 CPA 连接

已有 2.3.0 独立接入配置继续兼容。显式指定 `ACCESS_GUARD_BASE_URL` 和 `ACCESS_GUARD_MANAGEMENT_KEY_FILE`（或通过环境变量 `ACCESS_GUARD_MANAGEMENT_KEY`）即可覆盖默认 CPAMP 代理连接。公开范围仍选择上述三种方式之一。

原 `docker-compose.access-guard.yml` / `docker-compose.access-guard.acceptance.yml` 继续提供独立连接和文件挂载。管理密钥及名单文件建议设为 `root:65532`、`0640`；修改文件后要强制重建容器。`access-guard-public-keys.example.json` 是空名单模板。

Viewer 始终只请求固定 `GET /v0/management/plugins/access-guard/native-key-bindings`，再生成展示字段响应；不提供通用代理或任何额度修改接口。CPAMP 代理读取失败时不会自动尝试猜测 CPA 地址或混用密钥。

## 准备密钥

创建目录：

```bash
mkdir -p secrets
```

写入现有 CPAMP Admin Key：

```bash
read -rsp 'CPAMP Admin Key: ' viewer_admin_input; echo
printf '%s' "$viewer_admin_input" > secrets/cpamp_admin_key.txt
unset viewer_admin_input
```

生成 Viewer 内部分页游标签名密钥（不是登录密码）：

```bash
openssl rand -base64 48 > secrets/viewer_session_secret.txt
```

不要把 `secrets/` 提交到 Git。项目的 `.dockerignore` 已排除 `secrets/` 和 `.env*`，避免密钥进入 Docker 构建上下文。镜像以 UID/GID `65532` 的非 root 用户运行；Compose 的文件型 secret 是只读 bind mount，因此 Linux 主机应执行：

```bash
chown root:65532 secrets/*.txt
chmod 0640 secrets/*.txt
```

这样 root 与 Viewer 容器组可以读取，其他本机用户不能读取，也可避免 `/run/secrets/*: permission denied`。

## 确认现有 Docker 网络

运行：

```bash
docker inspect cpa-manager-plus \
  --format '{{range $name, $config := .NetworkSettings.Networks}}{{$name}}{{println}}{{end}}'
```

假设输出：

```text
mycpa_cliproxy
```

复制环境模板：

```bash
cp .env.example .env
```

编辑 `.env`：

```env
CPAMP_VIEWER_VERSION=2.6.0
VIEWER_PUBLIC_ACCESS=true
CLIPROXY_NETWORK=mycpa_cliproxy
CPAMP_BASE_URL=http://cpa-manager-plus:18317
CPAMP_VIEWER_BIND=127.0.0.1
CPAMP_VIEWER_PORT=18417
VIEWER_SECURE_COOKIES=false
CPAMP_ADMIN_KEY_PATH=./secrets/cpamp_admin_key.txt
VIEWER_SESSION_SECRET_PATH=./secrets/viewer_session_secret.txt
```

`.env.example` 默认使用 `bridge` 访问远程 CPAMP。连接同机 Docker 服务时，改为上面查询到的实际网络名。

如果连接另一台机器上已经运行的 CPAMP，可改为它的管理地址，例如：

```env
CPAMP_BASE_URL=http://192.0.2.10:18317
CLIPROXY_NETWORK=bridge
```

Compose 使用 `network_mode` 加入 Docker 网络。这里使用 Docker 自带的 `bridge` 网络即可访问同一局域网内的 CPAMP；只有与 CPAMP 同属一套 Compose 部署时，才需要填写它所在的专用 Docker 网络。

公开模式不使用登录 Cookie，因此 `VIEWER_SECURE_COOKIES` 不影响访问。只有显式改为 `VIEWER_PUBLIC_ACCESS=false`、重新启用密码登录时，HTTPS 域名才需要设置：

```env
VIEWER_SECURE_COOKIES=true
```

## 拉取、构建并启动

从 2.1.0 / 2.2.0 / 2.3.0 升级时，原有页面不需要修改数据库或密钥；启用 Key 额度页只需上文的公开范围设置。在线镜像名为 `jinshenganyuci/cpamp-viewer:2.6.0`；离线包保留本地标签 `cpamp-viewer:2.6.0`。

使用离线包时，先执行下文的 `docker load`，再把 `.env` 中的 `CPAMP_VIEWER_VERSION` 改为 `2.6.0` 并运行 `docker compose -f docker-compose.viewer.yml up -d --no-build`。Viewer 自身不保存请求历史，因此容器重建不会丢失运行期间的数据；历史仍来自现有 CPAMP。

在线镜像直接拉取：

```bash
docker compose pull cpamp-viewer
docker compose up -d --no-deps cpamp-viewer
```

通过叠加文件启用 Sub2API 时使用上文的双文件命令。

从源码构建：

```bash
docker compose -f docker-compose.viewer.yml build
docker compose -f docker-compose.viewer.yml up -d
```

如果使用随项目提供的离线镜像，先校验并加载镜像，再显式禁止 Compose 重新构建：

```bash
sha256sum -c cpamp-viewer_2.6.0_linux_amd64.tar.gz.sha256
docker load -i cpamp-viewer_2.6.0_linux_amd64.tar.gz
docker compose -f docker-compose.viewer.yml up -d --no-build
```

完整 Docker 部署包同时包含在线、离线和验收 Compose、`compose.sub2api.yaml`、`.env.example`、README、VERSION、所有许可证、离线镜像和包内 `SHA256SUMS`：

```bash
sha256sum -c cpamp-viewer_2.6.0_deployment.tar.gz.sha256
tar -xzf cpamp-viewer_2.6.0_deployment.tar.gz
cd cpamp-viewer_2.6.0_deployment
sha256sum -c SHA256SUMS
cp .env.example .env
```

部署包的 `secrets/README.txt` 只说明应创建的文件和权限，不包含任何真实 secret。

默认访问：

```text
http://127.0.0.1:18417/management.html#/
```

若旧版已经占用 `18417`，可用验收 Compose 把 2.6.0 并行启动在 `18418`。从源码目录验收时运行：

```bash
CPAMP_ADMIN_KEY_PATH=./secrets/cpamp_admin_key.txt \
VIEWER_SESSION_SECRET_PATH=./secrets/viewer_session_secret.txt \
docker compose -f docker-compose.acceptance.yml up -d --build
```

验收 Sub2API 时也追加 `-f compose.sub2api.yaml`。

在已加载镜像的离线部署包中运行：

```bash
docker compose -f docker-compose.acceptance.yml up -d --no-build
```

验收地址：

```text
http://服务器IP:18418/management.html#/
```

该 Compose 固定使用独立项目名 `cpamp-viewer-acceptance`、独立容器名和 `18418` 端口，不会停止、重建或成为 `18417` 旧 Viewer 的 Compose orphan。验收完成后只停止验收实例：

```bash
docker compose -f docker-compose.acceptance.yml down
```

### Windows 连接方式

本次发布中的 Windows 程序为交叉编译产物，未做 Windows 实机验收。以下为已有启动方法；Sub2API 的 Docker 部署使用前述叠加 Compose，不使用仅配置 CPAMP 的双击脚本。

在 Windows + Docker Desktop 环境中，双击：

```text
启动真实数据测试.cmd
```

脚本启动后会询问 CPAMP 地址，将 2.6.0 启动在本机 `18418`，并使用独立容器名，不会删除 `18417` 旧容器。它会先校验并加载随包提供的离线镜像，再提示输入 CPAMP 管理密钥；Viewer 页面无需登录密码。需要更换地址、端口或对局域网开放时可在 PowerShell 中运行：

```powershell
.\start-remote-test.ps1 -Upstream 'http://其他地址:18317' -Port 18418 -BindAddress '0.0.0.0'
```

Docker Desktop 双击脚本位于 `cpamp-viewer_2.6.0_deployment` 包。原生 Windows 程序位于 `cpamp-viewer_2.6.0_windows_amd64.tar.gz`；先校验它的同名 `.sha256`，解压后再校验包内 `SHA256SUMS`。GitHub Release 以各归档旁的 `.tar.gz.sha256` 和总 `SHA256SUMS` 校验下载文件；解压后用包内 `SHA256SUMS` 校验内容。

原生 Windows EXE 与 Docker Desktop 离线镜像是两种独立运行方式：

- `cpamp-viewer.exe`：原生 Windows 服务程序。
- `cpamp-viewer-remote-test.exe`：为现有脚本保留的同一程序副本。
- `cpamp-viewer-healthcheck.exe`：默认检查 `http://127.0.0.1:18417/health`；其他端口请设置 `HEALTHCHECK_URL`。
- `cpamp-viewer_2.6.0_linux_amd64.tar.gz`：供 Docker Desktop / Linux Docker 使用的离线镜像。

查看状态：

```bash
docker compose -f docker-compose.viewer.yml ps
docker compose -f docker-compose.viewer.yml logs -f cpamp-viewer
```

停止 Viewer：

```bash
docker compose -f docker-compose.viewer.yml down
```

通过叠加文件启用 Sub2API 时，上述 `ps`、`logs`、`down` 同样追加 `-f compose.sub2api.yaml`。

这不会停止或修改 `cli-proxy-api`、`cpa-manager-plus` 及其数据卷。

## 反向代理示例

将 `viewer.example.com` 反代到：

```text
http://127.0.0.1:18417
```

Nginx 最小示例：

```nginx
server {
    listen 443 ssl http2;
    server_name viewer.example.com;

    location / {
        proxy_pass http://127.0.0.1:18417;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        # Viewer 当前不信任 X-Forwarded-For；访问控制应在代理/CDN 层完成。
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

公开模式仍建议启用 HTTPS，以避免浏览内容被链路旁观者篡改或窃听。

## 本地开发与测试

前端：

```bash
cd web-cpamp
npm ci --ignore-scripts
npm run type-check
npm run test
npm run lint
npm run build
```

后端：

```bash
cd server
go test ./...
go build .
```

项目自带 `tests/mock-cpamp/main.go`，用于在不连接真实 CPAMP 的情况下验证公开访问、配额、用量分析和请求监控。

完整发布构建只需 Bash 和 Docker；Node、npm 与 Go 都由固定 digest 的构建镜像提供：

```bash
./scripts/build-release.sh
```

脚本会从干净依赖开始执行前端检查、危险管理路径扫描、Go 测试、`linux/amd64` 镜像构建、Windows `amd64` 交叉编译、镜像内许可证验证和 SHA-256 生成。发布输出位于 `release/2.6.0/`，其中包含：

- 独立 Linux `amd64` 离线镜像及校验文件。
- 原生 Windows `amd64` 目录、标准化归档、包内 `SHA256SUMS` 和外层归档校验。
- 只通过 allowlist 收集的 Docker 部署目录、标准化归档和双层 SHA-256。
- GitHub Release 的总 `SHA256SUMS` 覆盖三个下载归档和各自校验文件；源码构建另保留 Windows 文件清单用于本地分发。

默认 `SOURCE_DATE_EPOCH=0` 会固定 BuildKit 和外层 tar 时间戳；可以显式设置另一个固定值，但同一版本的重复构建必须使用相同值。不要直接压缩整个项目目录，因为本地测试目录可能含真实环境配置和对话记录。

## 许可证与归属

运行镜像、Docker 部署包和 Windows 包都包含 `licenses/`，其中至少包含 CPA Manager Plus MIT License/UPSTREAM 说明、ECharts Apache-2.0 LICENSE/NOTICE、zrender BSD-3-Clause 以及实际打包的 React 等 MIT 依赖许可证。

## 已知边界

- Sub2API 仅补充配额页，不能单独替代 CPAMP；缺少有效采样时不能据此得知实时额度。
- `/health` 同时检查 Viewer 与 CPAMP；CPAMP 不可达时返回 HTTP 503，Docker 健康检查会将容器标记为 unhealthy。
- Codex 配额优先读取完整 `wham/usage`，失败且没有成功缓存时才回退到已有快照；其他已支持提供方优先读取 CPAMP 已保存的配额快照。缺少额度数据时不会假定为 100%。
- Viewer 不执行 Codex reset-credit consumption、账号启停、认证文件修改、OAuth、插件管理、usage import/export 或模型价格写入。
- 默认公开模式意味着任何拿到访问地址的人都能看到六个页面中经过脱敏的数据；需要限定朋友范围时，应在反向代理层增加 Access List。
- 成本是 CPAMP 根据模型价格计算的估算值，不等同于 provider 发票。
- CPAMP API 发生不兼容变更时，需要更新 Viewer 的适配层；建议生产环境固定 CPAMP 和 Viewer 镜像版本，不使用 `latest`。
