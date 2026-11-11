# 起一个真的 MySQL 兼容服务（只读验证用）

同样是**不拉 Docker Hub**：本地 `golang:1.25` + apt 清华源装 `mariadb-server`（协议兼容 MySQL）。

```sh
docker build -t anc-mysql dev/mysql
docker run -d --name anc-my -p 13306:3306 anc-mysql
# 库 shop · 表 orders(3 行) · 账号 reader / reader-pass（mysql_native_password）
```

数据源定义：

```json
{"schema":"anc.ds/v1","name":"demo-mysql","type":"mysql",
 "dsn":"mysql://reader@127.0.0.1:13306/shop","secret":"MYSQL_DEMO_PASSWORD",
 "read_only":true,"timeout":"10s","max_rows":50}
```

已知限制：客户端只支持 `mysql_native_password`；服务端要求 `caching_sha2_password`
的完整认证（MySQL 8 默认）时会明确报错，不会假装成功。
