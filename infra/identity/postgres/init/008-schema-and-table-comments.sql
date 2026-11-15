\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE identity_migrator;

COMMENT ON SCHEMA identity IS '用户账号、OAuth/OIDC 登录授权与微信绑定核心身份域，由身份认证服务负责写入。';
COMMENT ON TABLE identity.authorization_codes IS 'OAuth/OIDC 登录授权码及一次性核销与过期状态，由身份认证服务维护。';
COMMENT ON TABLE identity.authorization_requests IS 'OIDC 登录发起阶段的授权挑战、PKCE 参数与防重放计数，由身份认证服务维护。';
COMMENT ON TABLE identity.security_audit_intents IS '用户创建、多因子认证与提权验证等安全操作待归档审计意图，由身份认证服务写入。';
COMMENT ON TABLE identity.security_outbox IS '安全审计事件可靠投递发件箱，由身份认证服务异步推送给审计账本。';
COMMENT ON TABLE identity.user_mfa IS '用户多因子认证密钥密文、失败锁定阈值与验证状态，由身份认证服务维护。';
COMMENT ON TABLE identity.users IS '平台用户基础账号、Argon2id 密码散列、状态及登录失败锁定计数，由身份认证服务维护。';
COMMENT ON TABLE identity.wechat_bindings IS '平台用户账号与微信应用 OpenID 的绑定关系，由身份认证服务维护。';

RESET ROLE;

COMMIT;
