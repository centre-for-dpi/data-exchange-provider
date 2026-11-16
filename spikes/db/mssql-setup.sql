-- Spike database (throwaway): synthetic data only, deterministic, no real people.
-- One base table the entity keeps private, two views it could expose, one table the reader
-- cannot see, and a read-only login that can SELECT only the views.
IF DB_ID('rid_spike') IS NOT NULL
BEGIN
  ALTER DATABASE rid_spike SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
  DROP DATABASE rid_spike;
END
GO
CREATE DATABASE rid_spike;
GO
USE rid_spike;
GO
CREATE SCHEMA salud;
GO
CREATE TABLE salud.personas_base (
  persona_id        int            NOT NULL PRIMARY KEY,
  tipo_documento    varchar(3)     NOT NULL,
  numero_documento  varchar(20)    NOT NULL UNIQUE,
  nombres           nvarchar(80)   NOT NULL,
  eps               nvarchar(60)   NOT NULL,
  regimen           varchar(20)    NOT NULL,
  estado            varchar(20)    NOT NULL,
  fecha_afiliacion  date           NOT NULL,
  actualizado_en    datetimeoffset NOT NULL,
  puntaje           decimal(6,2)   NOT NULL,
  activo            bit            NOT NULL,
  hogar_id          int            NOT NULL
);
GO
WITH n AS (
  SELECT TOP (1000) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS i
  FROM sys.all_objects a CROSS JOIN sys.all_objects b
)
INSERT INTO salud.personas_base
SELECT i,
       CASE WHEN i % 10 = 0 THEN 'CE' ELSE 'CC' END,
       CAST(1000000000 + i AS varchar(20)),
       CONCAT(N'Persona sintética ', i),
       CHOOSE(i % 5 + 1, N'EPS Uno', N'EPS Dos', N'EPS Tres', N'EPS Cuatro', N'EPS Cinco'),
       CASE WHEN i % 2 = 0 THEN 'contributivo' ELSE 'subsidiado' END,
       CASE WHEN i % 7 = 0 THEN 'retirado' ELSE 'activo' END,
       DATEADD(day, -((i * 37) % 3650), '2026-01-01'),
       DATEADD(minute, i, CAST('2026-11-01T08:00:00-05:00' AS datetimeoffset)),
       CAST((i * 13) % 10000 AS decimal(6,2)) / 100,
       CASE WHEN i % 7 = 0 THEN 0 ELSE 1 END,
       (i + 1) / 2
FROM n;
GO
-- Exposable view keyed by the person's document number.
CREATE VIEW salud.v_afiliados AS
SELECT tipo_documento, numero_documento, eps, regimen, estado, fecha_afiliacion, actualizado_en
FROM salud.personas_base;
GO
-- Exposable view whose natural key repeats: two people per household.
CREATE VIEW salud.v_hogares AS
SELECT hogar_id, numero_documento, puntaje, activo
FROM salud.personas_base;
GO
-- Something the reader must never see.
CREATE TABLE salud.notas_internas (id int PRIMARY KEY, nota nvarchar(200));
GO
-- Read-only login: SELECT on the two views only, nothing else.
IF EXISTS (SELECT 1 FROM sys.server_principals WHERE name = 'rid_reader') DROP LOGIN rid_reader;
-- The password comes from sqlcmd: -v READER_PASSWORD="..." (never stored in the repo).
CREATE LOGIN rid_reader WITH PASSWORD = '$(READER_PASSWORD)', CHECK_POLICY = OFF;
CREATE USER rid_reader FOR LOGIN rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
GRANT SELECT ON salud.v_hogares TO rid_reader;
GO
