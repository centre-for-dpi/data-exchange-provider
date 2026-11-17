#!/bin/bash
# Loads seed.ora (not *.sql, so the entrypoint does not run it twice) into the PDB
# ($DEN_ORACLE_PDB: FREEPDB1 on 23ai, XEPDB1 on 21c XE) and creates the users with the passwords
# from the environment. 11g XE has no PDB: see ../oracle11.
set -e
sqlplus -s / as sysdba <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE
ALTER SESSION SET CONTAINER = ${DEN_ORACLE_PDB:-FREEPDB1};
CREATE USER salud NO AUTHENTICATION QUOTA UNLIMITED ON users;
@/container-entrypoint-initdb.d/seed.ora
CREATE USER den_reader IDENTIFIED BY "$DEN_READER_PASSWORD";
GRANT CREATE SESSION TO den_reader;
GRANT SELECT ON salud.v_afiliados TO den_reader;
GRANT SELECT ON salud.v_hogares TO den_reader;
GRANT SELECT ON salud.v_tipos TO den_reader;
GRANT SELECT ON salud.v_codigos TO den_reader;
GRANT SELECT ON salud.v_claves TO den_reader;
GRANT SELECT ON salud.v_decimales TO den_reader;
CREATE USER den_writer IDENTIFIED BY "$DEN_WRITER_PASSWORD";
GRANT CREATE SESSION TO den_writer;
GRANT SELECT, INSERT, UPDATE, DELETE ON salud.escritura TO den_writer;
EXIT
EOF
