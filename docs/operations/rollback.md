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

脚本把清单里的 `before-<时间>` 标回现在的镜像名（一般是 `0.1.0-dev`），再执行重启脚本。登录用户不是 root 时，它和部署一样用 `sudo -n`。

## 重启脚本

重启脚本在仓库里：`deploy/platform/phase1/cd/deploy.sh`；它用 `node scripts/local-environment.mjs administrator --no-build` 建第一个管理员（以前是服务器上单独的 `administrator.mjs`）。每次部署随源码解到 `/opt/hvac-platform/deploy/platform/phase1/cd/`，部署和 `rollback.sh` 默认都跑这一份，并把这次发布的提交号传给它（网页的 `HVAC_WEB_BUILD_ID` 取前 12 位；不传时读 `/opt/hvac-platform/REVISION`）。

以前它只放在服务器的 `/opt/hvac-platform-tools/deploy.sh`，仓库里没有。那份现在是后备：仓库里这份不存在，或者登录用户不是 root 而 `sudo -n -l` 不允许免密跑它时，就退回去用老的那份，输出里会有一行 `restart script: <路径>` 写明用了哪一份。要强制用某一份，设 `HVAC_DEPLOY_SCRIPT=<路径>`。

如果 `sudoers` 只给 `/opt/hvac-platform-tools/deploy.sh` 开了免密，部署会一直走后备。要换成仓库里这份，得在 `sudoers` 里给 `/opt/hvac-platform/deploy/platform/phase1/cd/deploy.sh` 也开免密。注意这个文件归 `ubuntu` 所有，`ubuntu` 能改它，等于让 `ubuntu` 能以 root 跑任意命令；`ubuntu` 本来就有免密 `sudo ALL` 时没有区别。改仓库里的 `deploy.sh` 时，老的那份不会跟着变。

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
sudo -n /opt/hvac-platform/deploy/platform/phase1/cd/deploy.sh
```

`sudo` 不允许时用后备 `sudo -n /opt/hvac-platform-tools/deploy.sh`（见上面「重启脚本」）。

## 退回做不到的事

镜像退回之后，跑的是上一版程序。`/opt/hvac-platform` 里的 Compose 和配置已经是新提交解出来的那一版，脚本不会把目录也解回去。新配置配旧程序起不来时，要再部署一个已知能一起工作的提交，而不是只改标签。

Prometheus 和 Alertmanager 的配置也一样：退回不会把它们解回旧版，也不会重启它们。要回到旧的告警规则，部署旧提交（或者一个改回去的新提交），见 `docs/operations/alerts.md#发布时配置怎么生效`。

成功的部署只留最近两套 `before-*`。更老的标签会被删掉，不能再退到那一次。

每个镜像还有一个 `<名字>:deploy-base` 标签：部署都从它构建新镜像，而不是从正在跑的镜像构建，所以每次部署不会再多叠一层。这个标签在就用它，退回到一个在旧基础上构建的 `before-*` 之后，下次部署仍从现在的 `deploy-base` 构建，不会把旧基础找回来。不要删它；删了也不致命，下次部署会按镜像上的 `org.hvac.deploy-base` 标签找回，找不到就把当时正在跑的镜像当作新的基础。基础本身太大时，用部署流水线的 `rebuild_base` 重建，见 `docs/operations/disk-full.md#重建部署基础镜像`。成功部署后只清掉两小时以前的构建缓存；退回只是重打标签，用不到构建缓存。

退回不会恢复数据库里已经写入的数据。程序退回去之后，库还是新程序写过的样子。数据要回到备份，用 `docs/operations/restore-from-backup.md`。
