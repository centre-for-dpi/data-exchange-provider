-- Spike database (throwaway), PostgreSQL: same synthetic data and views as mssql-setup.sql.
-- Run by postgres-init.sh with -v reader_pw=... (the password never lives in the repo).
CREATE SCHEMA salud;
CREATE TABLE salud.personas_base (
  persona_id       integer PRIMARY KEY,
  tipo_documento   varchar(3)   NOT NULL,
  numero_documento varchar(20)  NOT NULL UNIQUE,
  nombres          varchar(80)  NOT NULL,
  eps              varchar(60)  NOT NULL,
  regimen          varchar(20)  NOT NULL,
  estado           varchar(20)  NOT NULL,
  fecha_afiliacion date         NOT NULL,
  actualizado_en   timestamptz  NOT NULL,
  puntaje          numeric(6,2) NOT NULL,
  activo           boolean      NOT NULL,
  hogar_id         integer      NOT NULL
);
INSERT INTO salud.personas_base
SELECT i,
       CASE WHEN i % 10 = 0 THEN 'CE' ELSE 'CC' END,
       (1000000000 + i)::text,
       'Persona sintética ' || i,
       (ARRAY['EPS Uno','EPS Dos','EPS Tres','EPS Cuatro','EPS Cinco'])[i % 5 + 1],
       CASE WHEN i % 2 = 0 THEN 'contributivo' ELSE 'subsidiado' END,
       CASE WHEN i % 7 = 0 THEN 'retirado' ELSE 'activo' END,
       DATE '2026-01-01' - ((i * 37) % 3650),
       TIMESTAMPTZ '2026-11-01 08:00:00-05' + i * INTERVAL '1 minute',
       ((i * 13) % 10000)::numeric / 100,
       i % 7 <> 0,
       (i + 1) / 2
FROM generate_series(1, 1000) AS i;

CREATE VIEW salud.v_afiliados AS
SELECT tipo_documento, numero_documento, eps, regimen, estado, fecha_afiliacion, actualizado_en
FROM salud.personas_base;
CREATE VIEW salud.v_hogares AS
SELECT hogar_id, numero_documento, puntaje, activo FROM salud.personas_base;
CREATE TABLE salud.notas_internas (id integer PRIMARY KEY, nota varchar(200));

CREATE ROLE rid_reader LOGIN PASSWORD :'reader_pw';
GRANT USAGE ON SCHEMA salud TO rid_reader;
GRANT SELECT ON salud.v_afiliados, salud.v_hogares TO rid_reader;
