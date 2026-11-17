#!/bin/bash
# Loads seed.ora (not *.sql, so the entrypoint does not run it twice) into FREEPDB1 and creates
# the users with the passwords from the environment.
set -e
sqlplus -s / as sysdba <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE
ALTER SESSION SET CONTAINER = FREEPDB1;
@/container-entrypoint-initdb.d/seed.ora
CREATE USER rid_reader IDENTIFIED BY "$RID_READER_PASSWORD";
GRANT CREATE SESSION TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
GRANT SELECT ON salud.v_hogares TO rid_reader;
GRANT SELECT ON salud.v_tipos TO rid_reader;
GRANT SELECT ON salud.v_codigos TO rid_reader;
GRANT SELECT ON salud.v_claves TO rid_reader;
CREATE USER rid_writer IDENTIFIED BY "$RID_WRITER_PASSWORD";
GRANT CREATE SESSION TO rid_writer;
GRANT SELECT, INSERT, UPDATE, DELETE ON salud.escritura TO rid_writer;
EXIT
EOF
