-- Spike database (throwaway), MySQL 8.4: same synthetic data and views as mssql-setup.sql.
-- MySQL has no time zone type: actualizado_en is a DATETIME in UTC.
CREATE DATABASE salud;
USE salud;
CREATE TABLE personas_base (
  persona_id       INT PRIMARY KEY,
  tipo_documento   VARCHAR(3)   NOT NULL,
  numero_documento VARCHAR(20)  NOT NULL UNIQUE,
  nombres          VARCHAR(80)  NOT NULL,
  eps              VARCHAR(60)  NOT NULL,
  regimen          VARCHAR(20)  NOT NULL,
  estado           VARCHAR(20)  NOT NULL,
  fecha_afiliacion DATE         NOT NULL,
  actualizado_en   DATETIME     NOT NULL,
  puntaje          DECIMAL(6,2) NOT NULL,
  activo           TINYINT(1)   NOT NULL,
  hogar_id         INT          NOT NULL
);
INSERT INTO personas_base
WITH RECURSIVE n (i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
SELECT i,
       IF(i % 10 = 0, 'CE', 'CC'),
       CAST(1000000000 + i AS CHAR),
       CONCAT('Persona sintética ', i),
       ELT(i % 5 + 1, 'EPS Uno', 'EPS Dos', 'EPS Tres', 'EPS Cuatro', 'EPS Cinco'),
       IF(i % 2 = 0, 'contributivo', 'subsidiado'),
       IF(i % 7 = 0, 'retirado', 'activo'),
       DATE_SUB('2026-01-01', INTERVAL (i * 37) % 3650 DAY),
       TIMESTAMP('2026-11-01 13:00:00') + INTERVAL i MINUTE,
       ((i * 13) % 10000) / 100,
       IF(i % 7 = 0, 0, 1),
       FLOOR((i + 1) / 2)
FROM n;

CREATE VIEW v_afiliados AS
SELECT tipo_documento, numero_documento, eps, regimen, estado, fecha_afiliacion, actualizado_en
FROM personas_base;
CREATE VIEW v_hogares AS
SELECT hogar_id, numero_documento, puntaje, activo FROM personas_base;
CREATE TABLE notas_internas (id INT PRIMARY KEY, nota VARCHAR(200));
