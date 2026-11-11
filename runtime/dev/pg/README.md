# 起一个真的 PostgreSQL（只读验证用）

Docker Hub 在开发机上经常拉不动，所以这里**不拉官方镜像**：用本地 `golang:1.25`
（Debian trixie）装 `postgresql`，apt 走清华源。装完约 1.8GB，构建 1–2 分钟。

```sh
docker build -t anc-pg dev/pg
docker run -d --name anc-pg -p 15432:5432 anc-pg
# 容器里：PG 17，库 shop，表 orders(3 行)，账号 reader / reader-pass（scram-sha-256）
```

Docker Desktop 的源地址是网关 IP（192.168.65.1），默认 pg_hba 不放行，所以要补一行：

```sh
docker exec -u root anc-pg sh -c \
  'echo "host all all 0.0.0.0/0 scram-sha-256" >> /var/lib/pg/data/pg_hba.conf && \
   runuser -u postgres -- /usr/lib/postgresql/17/bin/pg_ctl -D /var/lib/pg/data reload'
```

然后：

```sh
anc db query <vault> demo-pg --sql "SELECT id, name, amount FROM orders ORDER BY id"
```

数据源定义（`<vault>/../datasources/demo-pg.json`）里的 DSN 只写账号与地址，
密码按名字引用（`PG_DEMO_PASSWORD`），值放 secrets。
