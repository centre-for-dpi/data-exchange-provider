#!/bin/bash
# Oracle 11g XE: no PDB, and no NO AUTHENTICATION (18c+): SALUD gets a random password and is
# locked. The seed is ../oracle/seed.ora, mounted at /seed.
set -e
sqlplus -s / as sysdba <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE
CREATE USER salud IDENTIFIED BY "S$(od -An -N8 -tx1 /dev/urandom | tr -d ' ')" ACCOUNT LOCK DEFAULT TABLESPACE users QUOTA UNLIMITED ON users;
@/seed/seed.ora
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
