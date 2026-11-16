-- Spike database (throwaway), Oracle 23ai Free (FREEPDB1): same synthetic data and views as
-- mssql-setup.sql. SALUD is a schema-only account (no password, nobody logs in as it).
CREATE USER salud NO AUTHENTICATION QUOTA UNLIMITED ON users;
CREATE TABLE salud.personas_base (
  persona_id       NUMBER(10) PRIMARY KEY,
  tipo_documento   VARCHAR2(3)   NOT NULL,
  numero_documento VARCHAR2(20)  NOT NULL UNIQUE,
  nombres          NVARCHAR2(80) NOT NULL,
  eps              NVARCHAR2(60) NOT NULL,
  regimen          VARCHAR2(20)  NOT NULL,
  estado           VARCHAR2(20)  NOT NULL,
  fecha_afiliacion DATE          NOT NULL,
  actualizado_en   TIMESTAMP WITH TIME ZONE NOT NULL,
  puntaje          NUMBER(6,2)   NOT NULL,
  activo           NUMBER(1)     NOT NULL,
  hogar_id         NUMBER(10)    NOT NULL
);
INSERT INTO salud.personas_base
SELECT LEVEL,
       CASE WHEN MOD(LEVEL, 10) = 0 THEN 'CE' ELSE 'CC' END,
       TO_CHAR(1000000000 + LEVEL),
       'Persona sintética ' || LEVEL,
       CASE MOD(LEVEL, 5) WHEN 0 THEN 'EPS Uno' WHEN 1 THEN 'EPS Dos' WHEN 2 THEN 'EPS Tres'
                          WHEN 3 THEN 'EPS Cuatro' ELSE 'EPS Cinco' END,
       CASE WHEN MOD(LEVEL, 2) = 0 THEN 'contributivo' ELSE 'subsidiado' END,
       CASE WHEN MOD(LEVEL, 7) = 0 THEN 'retirado' ELSE 'activo' END,
       DATE '2026-01-01' - MOD(LEVEL * 37, 3650),
       TIMESTAMP '2026-11-01 08:00:00 -05:00' + NUMTODSINTERVAL(LEVEL, 'MINUTE'),
       MOD(LEVEL * 13, 10000) / 100,
       CASE WHEN MOD(LEVEL, 7) = 0 THEN 0 ELSE 1 END,
       TRUNC((LEVEL + 1) / 2)
FROM dual CONNECT BY LEVEL <= 1000;
COMMIT;

CREATE VIEW salud.v_afiliados AS
SELECT tipo_documento, numero_documento, eps, regimen, estado, fecha_afiliacion, actualizado_en
FROM salud.personas_base;
CREATE VIEW salud.v_hogares AS
SELECT hogar_id, numero_documento, puntaje, activo FROM salud.personas_base;
CREATE TABLE salud.notas_internas (id NUMBER PRIMARY KEY, nota VARCHAR2(200));
