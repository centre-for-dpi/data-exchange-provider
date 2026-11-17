#!/bin/sh
# Loads seed.mysql (not *.sql, so the entrypoint does not run it twice) and creates the users with
# the passwords from the environment.
set -e
mysql --default-character-set=utf8mb4 -uroot -p"$MYSQL_ROOT_PASSWORD" < /docker-entrypoint-initdb.d/seed.mysql
mysql --default-character-set=utf8mb4 -uroot -p"$MYSQL_ROOT_PASSWORD" <<EOF
CREATE USER 'den_reader'@'%' IDENTIFIED BY '$DEN_READER_PASSWORD';
GRANT SELECT ON salud.v_afiliados TO 'den_reader'@'%';
GRANT SELECT ON salud.v_hogares TO 'den_reader'@'%';
GRANT SELECT ON salud.v_tipos TO 'den_reader'@'%';
GRANT SELECT ON salud.v_codigos TO 'den_reader'@'%';
GRANT SELECT ON salud.v_claves TO 'den_reader'@'%';
GRANT SELECT ON salud.v_decimales TO 'den_reader'@'%';
CREATE USER 'den_writer'@'%' IDENTIFIED BY '$DEN_WRITER_PASSWORD';
GRANT SELECT, INSERT, UPDATE, DELETE ON salud.escritura TO 'den_writer'@'%';
-- Writes through a role, and through a column-level grant.
CREATE ROLE 'escritores';
GRANT SELECT, INSERT ON salud.escritura TO 'escritores';
CREATE USER 'den_role_writer'@'%' IDENTIFIED BY '$DEN_WRITER_PASSWORD';
GRANT 'escritores' TO 'den_role_writer'@'%';
SET DEFAULT ROLE 'escritores' TO 'den_role_writer'@'%';
CREATE USER 'den_column_writer'@'%' IDENTIFIED BY '$DEN_WRITER_PASSWORD';
GRANT SELECT, UPDATE (nota) ON salud.escritura TO 'den_column_writer'@'%';
EOF
