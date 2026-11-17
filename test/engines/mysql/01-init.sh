#!/bin/sh
# Loads seed.mysql (not *.sql, so the entrypoint does not run it twice) and creates the users with
# the passwords from the environment.
set -e
mysql --default-character-set=utf8mb4 -uroot -p"$MYSQL_ROOT_PASSWORD" < /docker-entrypoint-initdb.d/seed.mysql
mysql --default-character-set=utf8mb4 -uroot -p"$MYSQL_ROOT_PASSWORD" <<EOF
CREATE USER 'rid_reader'@'%' IDENTIFIED BY '$RID_READER_PASSWORD';
GRANT SELECT ON salud.v_afiliados TO 'rid_reader'@'%';
GRANT SELECT ON salud.v_hogares TO 'rid_reader'@'%';
GRANT SELECT ON salud.v_tipos TO 'rid_reader'@'%';
CREATE USER 'rid_writer'@'%' IDENTIFIED BY '$RID_WRITER_PASSWORD';
GRANT SELECT, INSERT, UPDATE, DELETE ON salud.escritura TO 'rid_writer'@'%';
EOF
