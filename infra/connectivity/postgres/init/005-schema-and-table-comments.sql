\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE connectivity_migrator;

COMMENT ON SCHEMA connectivity IS '站点网关证书凭据、MQTT 指令关联与异常报文隔离域，由设备接入服务负责写入。';
COMMENT ON TABLE connectivity.command_reply_correlations IS '下发到站点网关的 MQTT 控制指令围栏关联与设备回执状态，由设备接入服务维护。';
COMMENT ON TABLE connectivity.gateway_credential_audit IS '站点网关一次性接入码生成及客户端证书签发、续期、吊销操作审计流水，由设备接入服务写入。';
COMMENT ON TABLE connectivity.gateway_credentials IS '站点网关 mTLS 客户端证书 SHA-256 指纹、有效时间窗与吊销状态，由设备接入服务维护。';
COMMENT ON TABLE connectivity.gateway_enrollments IS '站点网关一次性接入码散列、有效期及兑换状态，由设备接入服务维护。';
COMMENT ON TABLE connectivity.uplink_quarantine IS '因认证、格式或协议校验失败而被隔离的网关上行报文及原因，由设备接入服务写入。';

RESET ROLE;

COMMIT;
