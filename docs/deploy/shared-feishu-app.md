# 共享测试用 Feishu App(内部分发版)

> **谁该读这篇:** 拿到 parsar 仓库的内部同事,跟着 `INSTALL.md` 已经
> 用 mock auth 跑起来了,现在想体验真实的飞书登录 / 群机器人交互,但
> 又不想自己去飞书开放平台注册一个 App。
>
> **谁不该读这篇:** 准备把 parsar 部署给真实团队 / 客户用的同学,
> 请走 [`feishu-prod.md`](feishu-prod.md) 注册你自己的 App。共享 App
> 只给内部试用,不要对外公开它的回调域名,也不要把它发到群里。

---

## 为什么不直接把 App ID/Secret 写进仓库

`PARSAR_FEISHU_APP_SECRET` 是一段长期凭证。任何拿到它的人都可以:

- 冒充这个 App 调飞书 API(查通讯录、发消息);
- 解密这个 App 收到的事件回调;
- 在飞书开放平台后台看到的所有日志里"被署名为这个 App"。

仓库是开源的 / 分发给多人的,**任何 secret 入了 git 历史就视同泄露**
(即便后面 commit 删掉,reflog / fork / 已 clone 的同事本地还在),
必须走飞书后台 reset secret 流程。所以这份 App 的真实凭证只在飞书内
部渠道(下面 §1)分发,**永远不进 git**。

---

## 1. 从哪里拿到共享 App 的凭证

> **状态:** App ID 已填(下方表格);Secret / Verification Token /
> Encrypt Key **不进 git**,在 1Password 团队保险库的 `parsar-oss
> shared Feishu app` 条目里(owner 已分发,没看到条目请私聊 owner
> 加你访问权)。

| 字段 | 值 |
|---|---|
| `PARSAR_FEISHU_APP_ID` | `cli_a9488c841e79dcee` |
| `PARSAR_FEISHU_APP_SECRET` | 1Password → `parsar-oss shared Feishu app` → `app_secret` 字段 |
| `PARSAR_FEISHU_VERIFICATION_TOKEN` | 1Password → 同上条目 → `verification_token` 字段 |
| `PARSAR_FEISHU_ENCRYPT_KEY` | 1Password → 同上条目 → `encrypt_key` 字段(留空表示该 App 未启用事件加密) |
| `PARSAR_FEISHU_REDIRECT_URI` | 见下方 §2 |
| 共享 App 后台链接(给 owner 用) | `<待 owner 填写:open.feishu.cn 上的 App 后台 URL>` |
| 共享 App 所属租户 | `<待 owner 填写:e.g. xxx.feishu.cn>` |

**分发渠道建议(给 owner):**

- 优先用 1Password / 飞书企业密码本 / Bitwarden 共享条目,授权给特定
  成员;
- 兜底用飞书私聊点对点发,**不要发群**,**不要发邮件附件**;
- 不要写进任何会被截图 / 录屏 / 共享出去的文档(包括本文档本身)。

如果你打不开上面的 1Password 条目:私聊 owner 加访问权,**不要**让
其他同事截屏 / 复制 secret 给你 — 走密码管理器是唯一安全路径。

---

## 2. 回调地址(`REDIRECT_URI`)

共享 App 的回调白名单已经预先配置了几条本地常用地址。**你的
`PARSAR_PUBLIC_URL` 必须匹配其中一条**,否则飞书会拒绝回调。

预配置白名单:

| 场景 | `PARSAR_PUBLIC_URL` | `PARSAR_FEISHU_REDIRECT_URI` |
|---|---|---|
| 本机 docker compose 默认 | `http://localhost:8080` | `http://localhost:8080/api/v1/auth/feishu/callback` |
| 本机改了端口 | `http://localhost:<port>` | `http://localhost:<port>/api/v1/auth/feishu/callback` |
| 内网穿透 / 临时公网 | `<待 owner 填写:例如 https://parsar-test.example.com>` | 同上加 `/api/v1/auth/feishu/callback` |

**用 localhost 走的同事不需要内网穿透** — 飞书 OAuth 的浏览器重定向
回调走的是用户浏览器,不是飞书服务器主动回调你,所以 `localhost` 是
合法的(只要白名单里有)。

如果你需要的地址不在白名单里:私聊 owner 加,**不要自己改 App 后台
配置**(会影响所有用这个 App 的同事)。

---

## 3. 从 mock 切到共享 App

前置:已经按 [`../../INSTALL.md`](../../INSTALL.md) 把 parsar 用
mock 模式跑起来了,`deploy/compose/.env` 已存在并能正常 `docker
compose up`。

### 3.1 关掉 mock,填四个 Feishu 变量

```bash
cd deploy/compose

# 关掉 mock
sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=false/' .env

# 填四个 Feishu 变量(把 <…> 替换成从 owner 拿到的值)
cat >> .env <<'EOF'

# --- 共享测试 App(来自 docs/deploy/shared-feishu-app.md) ---
PARSAR_FEISHU_APP_ID=<cli_xxxxx>
PARSAR_FEISHU_APP_SECRET=<xxx>
PARSAR_FEISHU_VERIFICATION_TOKEN=<xxx>
PARSAR_FEISHU_ENCRYPT_KEY=
PARSAR_FEISHU_REDIRECT_URI=http://localhost:8080/api/v1/auth/feishu/callback
EOF

rm -f .env.bak
```

如果 `.env` 里这四个 `PARSAR_FEISHU_*` 行已经存在(比如之前填过),
**编辑覆盖,不要追加新的同名行** — docker compose 取的是最后一次出
现的值,但 `grep` 排错会一眼看到两份,容易误判。

### 3.2 重启 server,让新 env 生效

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env \
  up -d --force-recreate parsar-server
sleep 6
docker logs parsar-server 2>&1 | tail -20
```

**期望:** 日志里没有 `PARSAR_FEISHU_*_required` 之类的报错,server
状态 `Up X seconds (healthy)`。

### 3.3 浏览器验证

打开 `http://localhost:8080`,点「飞书登录」。期望:

1. 跳转到飞书 OAuth 授权页(域名是 `passport.feishu.cn` 或
   `accounts.feishu.cn`);
2. 同意授权后跳回 `http://localhost:8080/api/v1/auth/feishu/callback`;
3. 最终落到 parsar 首页,右上角显示的是**你真实的飞书姓名 / 头像**,
   不是 mock 模式的 `admin@example.com`。

---

## 4. 常见问题排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 飞书授权页报 `redirect_uri 不在白名单` | 你的 `PARSAR_PUBLIC_URL` 或 `PARSAR_FEISHU_REDIRECT_URI` 不在 §2 的白名单 | 改回 `http://localhost:8080`,或私聊 owner 加白名单 |
| 回调成功但落回页面报 `email scope required` | 共享 App 没申请 / 没审批 `contact:user.email:readonly` 权限 | 找 owner,这是 App 级别的配置,不是你 `.env` 能修的 |
| 日志反复出现 `verification token mismatch` | `.env` 里 `PARSAR_FEISHU_VERIFICATION_TOKEN` 填错了 | 找 owner 重新核对一次,飞书后台值是权威 |
| 登录成功,但群里 @ 机器人没反应 | 事件订阅没把你这台 parsar 当回调地址(共享 App 只能配一个回调地址) | 这是预期 — 共享 App 默认只把事件转到 owner 的 staging,不转你本机。要本机收事件请走 [`feishu-prod.md`](feishu-prod.md) 自己注册 App |
| 想切回 mock | `sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=true/' .env` + 重启 server | 共享 App 凭证留在 `.env` 里没关系,`MOCK=true` 时 server 会忽略它们 |

---

## 5. 退出 / 清理

试用完不想再连共享 App 时:

```bash
cd deploy/compose
# 把四个 Feishu 变量行删掉(或留着但把 MOCK 切回 true)
sed -i.bak '/^PARSAR_FEISHU_APP_ID=/d;
            /^PARSAR_FEISHU_APP_SECRET=/d;
            /^PARSAR_FEISHU_VERIFICATION_TOKEN=/d;
            /^PARSAR_FEISHU_ENCRYPT_KEY=/d;
            /^PARSAR_FEISHU_REDIRECT_URI=/d' .env
rm -f .env.bak

# 切回 mock(可选)
sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=true/' .env
rm -f .env.bak

# 重启
docker compose -f deploy/compose/compose.example.yml --env-file .env \
  up -d --force-recreate parsar-server
```

`.env` 本身 `.gitignore` 已经覆盖(根目录 `.gitignore` 第 7-9 行的
`.env` / `.env.*` 规则),不会进 git;但如果你把 secrets 复制到了别
处(临时记事本、聊天记录),记得也清掉。

---

## 6. 真要做正式部署?

不要继续用共享 App。共享 App 是"内部多人试用"用途,**它的回调地址、
权限范围、租户都是 owner 的**,真实团队部署必然需要:

- 你自己租户下的 App(不然给团队用的人都得加入 owner 的飞书租户);
- 你自己拥有的回调域名 + HTTPS;
- 你自己的 secret manager,不再依赖私聊分发。

完整流程见 [`feishu-prod.md`](feishu-prod.md)。
