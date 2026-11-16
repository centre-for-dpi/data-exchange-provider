#!/bin/sh
# Spike: loads mysql.sql and creates the read-only user with the password from the environment.
set -e
mysql -uroot -p"$MYSQL_ROOT_PASSWORD" < /sql/mysql.sql
mysql -uroot -p"$MYSQL_ROOT_PASSWORD" <<EOF
CREATE USER 'rid_reader'@'%' IDENTIFIED BY '$RID_READER_PASSWORD';
GRANT SELECT ON salud.v_afiliados TO 'rid_reader'@'%';
GRANT SELECT ON salud.v_hogares TO 'rid_reader'@'%';
EOF
