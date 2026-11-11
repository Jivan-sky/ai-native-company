#!/bin/sh
set -e
mkdir -p /run/mysqld && chown mysql:mysql /run/mysqld
if [ ! -d /var/lib/mysql/mysql ]; then
  mariadb-install-db --user=mysql --datadir=/var/lib/mysql >/tmp/install.log 2>&1
fi
mariadbd --user=mysql --datadir=/var/lib/mysql --bind-address=0.0.0.0 --port=3306 &
for i in $(seq 1 90); do mariadb -e 'select 1' >/dev/null 2>&1 && break; sleep 1; done
mariadb -e "CREATE DATABASE IF NOT EXISTS shop"
mariadb -e "CREATE USER IF NOT EXISTS 'reader'@'%' IDENTIFIED BY 'reader-pass'"
mariadb -e "GRANT SELECT ON shop.* TO 'reader'@'%'"
mariadb shop -e "CREATE TABLE IF NOT EXISTS orders(id int primary key, name varchar(64), amount decimal(10,2))"
mariadb shop -e "INSERT IGNORE INTO orders VALUES (1,'alice',18.00),(2,'bob',42.50),(3,'carol',7.25)"
echo "=== mysql init done ==="
wait
