# 把一次部署退回去

生产部署给换掉的镜像留着 `before-<时间>` 标签，时间是 UTC，例如 `hvac/energy-api:before-20261029T120000Z`。同一次部署的各个镜像用同一个时间。

## 用脚本

每次部署都会把对照写在 `/opt/hvac-platform/rollbacks/<时间>.tsv`，最近一次的时间写在同目录的 `latest`。

退回最近一次：

```bash
bash /opt/hvac-platform/deploy/platform/phase1/cd/rollback.sh
```

退回某一次：

```bash
bash /opt/hvac-platform/deploy/platform/phase1/cd/rollback.sh 20261029T120000Z
```

脚本把清单里的 `before-<时间>` 标回现在的镜像名（一般是 `0.1.0-dev`），再执行 `/opt/hvac-platform-tools/deploy.sh`。登录用户不是 root 时，它和部署一样用 `sudo -n`。

流水线自己也会这么做：服务器上的健康检查或 GitHub 机器上的公网检查失败，都会退回这一次的 `before-*` 再重启。

## 还没有清单时

更早的部署只有 Docker 标签，没有 `rollbacks/*.tsv`。看有哪些时间：

```bash
docker images --format '{{.Repository}}:{{.Tag}}' | grep ':before-' | sort
```

对每一个要退回的仓库，把那个时间标回现行标签，例如：

```bash
docker tag hvac/energy-api:before-20261029T120000Z hvac/energy-api:0.1.0-dev
```

网页镜像是 `hvac/hvac-web-real`，AI 助手是 `hvac/operations-agent-service`，迁移镜像是 `hvac/phase1-migrator`。同一分钟的标签一起标回去，不要只退其中一个服务却留着别的服务的新镜像。标完再：

```bash
sudo -n /opt/hvac-platform-tools/deploy.sh
```

## 退回做不到的事

镜像退回之后，跑的是上一版程序。`/opt/hvac-platform` 里的 Compose 和配置已经是新提交解出来的那一版，脚本不会把目录也解回去。新配置配旧程序起不来时，要再部署一个已知能一起工作的提交，而不是只改标签。

成功的部署只留最近两套 `before-*`。更老的标签会被删掉，不能再退到那一次。

退回不会恢复数据库里已经写入的数据。程序退回去之后，库还是新程序写过的样子。数据要回到备份，用 `docs/operations/restore-from-backup.md`。
