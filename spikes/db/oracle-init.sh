#!/bin/bash
# Spike: loads oracle.sql into FREEPDB1 and creates the read-only user (password from the env).
set -e
sqlplus -s / as sysdba <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE
ALTER SESSION SET CONTAINER = FREEPDB1;
@/sql/oracle.sql
CREATE USER rid_reader IDENTIFIED BY "$RID_READER_PASSWORD";
GRANT CREATE SESSION TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
GRANT SELECT ON salud.v_hogares TO rid_reader;
EXIT
EOF
