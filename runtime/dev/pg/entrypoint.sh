#!/bin/sh
set -e
PGBIN=/usr/lib/postgresql/17/bin
PGDATA=/var/lib/pg/data
if [ ! -f "$PGDATA/PG_VERSION" ]; then
  mkdir -p "$PGDATA" /var/lib/pg
  chown -R postgres:postgres /var/lib/pg
  runuser -u postgres -- "$PGBIN/initdb" -D "$PGDATA" --auth-local=trust --auth-host=scram-sha-256 --username=postgres >/tmp/initdb.log 2>&1
  echo "listen_addresses='*'" >> "$PGDATA/postgresql.conf"
  runuser -u postgres -- "$PGBIN/pg_ctl" -D "$PGDATA" -l /tmp/pg.log -w start
  runuser -u postgres -- "$PGBIN/psql" -q -c "CREATE USER reader WITH PASSWORD 'reader-pass'"
  runuser -u postgres -- "$PGBIN/psql" -q -c 'CREATE DATABASE shop'
  runuser -u postgres -- "$PGBIN/psql" -q -d shop -c 'ALTER DATABASE shop OWNER TO reader'
  runuser -u postgres -- "$PGBIN/psql" -q -d shop -c 'CREATE TABLE orders(id int primary key, name text, amount numeric(10,2))'
  runuser -u postgres -- "$PGBIN/psql" -q -d shop -c "INSERT INTO orders VALUES (1,'alice',18.00),(2,'bob',42.50),(3,'carol',7.25)"
  runuser -u postgres -- "$PGBIN/psql" -q -d shop -c 'ALTER TABLE orders OWNER TO reader'
  echo "=== init done ==="
else
  runuser -u postgres -- "$PGBIN/pg_ctl" -D "$PGDATA" -l /tmp/pg.log -w start
fi
tail -f /tmp/pg.log
