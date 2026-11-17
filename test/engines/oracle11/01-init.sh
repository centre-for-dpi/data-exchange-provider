#!/bin/bash
# Oracle 11g XE: no PDB, and no NO AUTHENTICATION (18c+): SALUD gets a random password and is
# locked. The seed is ../oracle/seed.ora, mounted at /seed.
set -e
sqlplus -s / as sysdba <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE
CREATE USER salud IDENTIFIED BY "S$(od -An -N8 -tx1 /dev/urandom | tr -d ' ')" ACCOUNT LOCK DEFAULT TABLESPACE users QUOTA UNLIMITED ON users;
@/seed/seed.ora
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
