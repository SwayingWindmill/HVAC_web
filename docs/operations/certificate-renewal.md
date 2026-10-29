# 证书续期

对外的 HTTPS 证书是 Let's Encrypt，签给 `hvac.swayingwindmill.online`。浏览器和网关看到的是主机 nginx，它再转到本机 `127.0.0.1:18443` 上的容器 nginx。续期换的是主机 nginx 用的那一对，不是容器里的网页镜像。

仓库里没有 certbot 的配置。续期命令以这台机器上已经在跑的定时任务为准。

## 看到期日

剩下不到 14 天会告警，不到 3 天是严重。也可以直接看：

```bash
echo | openssl s_client -servername hvac.swayingwindmill.online -connect 127.0.0.1:443 2>/dev/null | openssl x509 -noout -subject -enddate
```

`-enddate` 是到期时刻。例如还要两个月，就不用提前手工换。

## 找到主机上的证书文件

```bash
sudo nginx -T 2>/dev/null | grep -E 'ssl_certificate|server_name'
systemctl list-timers --all | grep -Ei 'cert|acme|letsencrypt'
```

`ssl_certificate` 那一行是全链文件，旁边的 `ssl_certificate_key` 是私钥。私钥不要拷进仓库，也不要贴进工单。

## 续期

用上面查到的定时任务或它的服务再跑一次（常见的是 `certbot renew`）。跑完看文件的修改时间已经变了，再用上一节的 `openssl` 命令确认新的到期日。

然后只重载主机 nginx，不需要重新部署应用：

```bash
sudo nginx -t && sudo systemctl reload nginx
```

重载后从外面看一次：

```bash
curl -fsS https://hvac.swayingwindmill.online/healthz
```

正文应是 `ok`。

## 另外两张不要和这张搞混

- 容器里的网页 nginx 用的是平台自己签的证书，浏览器不直接连它。
- 松雅轩网关连 MQTT 的客户端证书到 2027-01-12。那是设备证书，续期走网页「集成管理」里的网关证书，不是 Let's Encrypt。
