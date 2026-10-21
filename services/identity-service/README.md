# 身份服务（identity-service）

负责登录：校验平台账号的用户名和密码，并提供 `energy-api`（平台网关）需要的最基本的 OIDC 登录接口。

它**不管权限**。租户成员、角色指派、策略和明确禁止都由 IAM 管。两边交接只用一个不变的身份：OIDC 的「签发方 + 主体编号」。

## 做了什么

- 用户名和密码登录，密码用 Argon2id 加密保存。
- OIDC 发现接口；带 PKCE S256 的授权码流程；`state` 和 `nonce` 经网关的登录流程绑定；授权码一次性、短时有效。
- RS256 的 ID 令牌和 JWKS 公钥；最基本的退出跳转。
- 密码输错 5 次，锁定 15 分钟。
- 密码规则（规格 `docs/specs/iam-roles-and-users.md`，按 NIST SP 800-63B-4，最短 8 位）：8 个字符到 512 字节，不要求字符组合，不强制定期更换，不能和用户名相同，也不能是 `internal/identity/common-passwords.txt` 里的常见密码（SecLists 2024.4，MIT 许可）。
- 自己不出任何页面：`/authorize` 和每一种登录结果都跳到网页的 `/sign-in?login_challenge=…`（带 `step=totp` 或错误代码），网页的表单把密码或一次性验证码直接提交回 `/identity/login` 和 `/identity/mfa/totp`。挑战编号必须和 `__Host-hvac_idp_login` cookie 一致（`docs/specs/sign-in-page.md`）。
- 需要更高安全等级的登录可以要求一次性验证码（TOTP）；开通验证码只能离线用 `cmd/identity-admin` 做（`totp-start`、`totp-activate`、`totp-disable`）。
- 微信小程序登录（#664）：小程序跟不了浏览器的跳转、也存不住 cookie，由平台网关在内网替它调 `/app/login` 和 `/app/mfa/totp`；绑定了微信的人还可以用 `/app/wechat/login` 一键登录（换到微信编号、找到绑定的账号，令牌里的登录方式是 `wechat`）。密码规则、锁定、停用和验证码都和 `/login`、`/mfa/totp` 一样；只给平台自己的客户端和回调地址、带 PKCE 的请求发授权码，所以授权码只有网关能换。nginx 不对外转发 `/identity/app/`。
- 用户管理用 `pkg/identityadmin`：平台网关把它嵌在进程里，以 `identity_admin` 连接数据库，管理员在「系统管理 → 用户与角色」里新建账号、设密码、改姓名和邮箱、停用或启用登录；每个人都能改自己的密码。权限仍然归 IAM。没有公开的注册接口；`cmd/identity-admin` 留给第一个管理员和找回账号用。

故意不做：刷新令牌、第三方登录、通行密钥（Passkey）、邮件或短信登录、自助找回密码、SAML、企业身份联合、租户和站点权限。产品真需要时再加。

## 数据

账号、密码和登录过程中的临时数据存在单独的 PostgreSQL 库 `hvac_identity` 的 `identity` 表里。IAM 不保存任何密码。`identity_directory_reader` 这个数据库角色只能读 `id`、`display_name`、`email` 和 `status`，给身份同步用。

## 配置

必填的环境变量：

- `IDENTITY_ISSUER`
- `IDENTITY_CLIENT_ID`
- `IDENTITY_REDIRECT_URI`
- `IDENTITY_POST_LOGOUT_REDIRECT_URI`
- `IDENTITY_DATABASE_URL`
- `IDENTITY_SIGNING_KEY_FILE`

`IDENTITY_SIGNING_KEY_FILE` 指向部署时提供的 PKCS#8 RSA 私钥文件；文件不存在或无效时，服务拒绝启动。

可选的监听地址：

- `IDENTITY_ADDR`（默认 `:19095`）
- `IDENTITY_DIAGNOSTICS_ADDR`（默认 `:19085`）

第一阶段的 Compose 里，对外的登录地址由 nginx 在 `/identity/` 下提供，服务本身不另外开端口。

## 开通账号

`cmd/identity-admin` 是离线的管理命令，必须用专门的 `identity_admin` 数据库角色连接，不能用 `identity_runtime`。数据库地址和账号信息通过运行时的环境变量提供；不要把凭据或初始密码提交到 Git。

支持的操作：`create`、`reset-password`、`reset-password-random`、`totp-start`、`totp-activate`、`totp-disable`。随机重置会在命令里生成新密码，只打印一次。建好第一个管理员后，用 `modules/iam/cmd/identity-reconciler` 和一份给它整租户 `admin` 角色的种子，把它的「签发方 + 主体编号」同步进 IAM；之后的人都在系统管理里加。身份服务从不分配平台权限。

## 签名密钥

第一阶段只跑一个身份服务实例，从 `IDENTITY_SIGNING_KEY_FILE` 加载 RSA 签名密钥。`cmd/signing-keygen` 为部署的运行时目录生成 PKCS#8 RSA 密钥；私钥绝不提交。密钥编号（`kid`）由公钥推导，所以挂的是同一个密钥文件时，重启后 JWKS 不变。ID 令牌有效 5 分钟。换密钥是运维操作，要有计划地替换部署里的密钥文件。
