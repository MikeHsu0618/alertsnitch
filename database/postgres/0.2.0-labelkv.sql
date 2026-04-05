-- Deduplicate label and annotation key/value pairs via lookup tables.
-- KvHash is MD5(hex) of key || ASCII SOH || value so unique indexes stay within engine limits (NUL is invalid in PG text).

CREATE TABLE LabelKV (
    ID SERIAL NOT NULL PRIMARY KEY,
    LabelKey VARCHAR(100) NOT NULL,
    Value VARCHAR(1000) NOT NULL,
    KvHash CHAR(32) NOT NULL,
    CONSTRAINT LabelKV_KvHash_key UNIQUE (KvHash)
);

CREATE TABLE AnnotationKV (
    ID SERIAL NOT NULL PRIMARY KEY,
    AnnotationKey VARCHAR(100) NOT NULL,
    Value VARCHAR(1000) NOT NULL,
    KvHash CHAR(32) NOT NULL,
    CONSTRAINT AnnotationKV_KvHash_key UNIQUE (KvHash)
);

INSERT INTO LabelKV (LabelKey, Value, KvHash)
SELECT DISTINCT gl.grouplabel, gl.value, md5(gl.grouplabel || chr(1) || gl.value)
FROM GroupLabel gl
ON CONFLICT (KvHash) DO NOTHING;

INSERT INTO LabelKV (LabelKey, Value, KvHash)
SELECT DISTINCT cl.label, cl.value, md5(cl.label || chr(1) || cl.value)
FROM CommonLabel cl
ON CONFLICT (KvHash) DO NOTHING;

INSERT INTO LabelKV (LabelKey, Value, KvHash)
SELECT DISTINCT al.label, al.value, md5(al.label || chr(1) || al.value)
FROM AlertLabel al
ON CONFLICT (KvHash) DO NOTHING;

INSERT INTO AnnotationKV (AnnotationKey, Value, KvHash)
SELECT DISTINCT ca.annotation, ca.value, md5(ca.annotation || chr(1) || ca.value)
FROM CommonAnnotation ca
ON CONFLICT (KvHash) DO NOTHING;

INSERT INTO AnnotationKV (AnnotationKey, Value, KvHash)
SELECT DISTINCT aa.annotation, aa.value, md5(aa.annotation || chr(1) || aa.value)
FROM AlertAnnotation aa
ON CONFLICT (KvHash) DO NOTHING;

ALTER TABLE GroupLabel ADD COLUMN LabelKVID INTEGER;
UPDATE GroupLabel gl SET LabelKVID = lk.ID
FROM LabelKV lk
WHERE lk.LabelKey = gl.grouplabel AND lk.Value = gl.value;
ALTER TABLE GroupLabel DROP COLUMN grouplabel, DROP COLUMN value;
ALTER TABLE GroupLabel ALTER COLUMN LabelKVID SET NOT NULL;
ALTER TABLE GroupLabel
    ADD CONSTRAINT GroupLabel_LabelKVID_fkey FOREIGN KEY (LabelKVID) REFERENCES LabelKV (ID);
CREATE UNIQUE INDEX GroupLabel_alertgroup_labelkv_idx ON GroupLabel (AlertGroupID, LabelKVID);

ALTER TABLE CommonLabel ADD COLUMN LabelKVID INTEGER;
UPDATE CommonLabel cl SET LabelKVID = lk.ID
FROM LabelKV lk
WHERE lk.LabelKey = cl.label AND lk.Value = cl.value;
ALTER TABLE CommonLabel DROP COLUMN label, DROP COLUMN value;
ALTER TABLE CommonLabel ALTER COLUMN LabelKVID SET NOT NULL;
ALTER TABLE CommonLabel
    ADD CONSTRAINT CommonLabel_LabelKVID_fkey FOREIGN KEY (LabelKVID) REFERENCES LabelKV (ID);
CREATE UNIQUE INDEX CommonLabel_alertgroup_labelkv_idx ON CommonLabel (AlertGroupID, LabelKVID);

ALTER TABLE CommonAnnotation ADD COLUMN AnnotationKVID INTEGER;
UPDATE CommonAnnotation ca SET AnnotationKVID = ak.ID
FROM AnnotationKV ak
WHERE ak.AnnotationKey = ca.annotation AND ak.Value = ca.value;
ALTER TABLE CommonAnnotation DROP COLUMN annotation, DROP COLUMN value;
ALTER TABLE CommonAnnotation ALTER COLUMN AnnotationKVID SET NOT NULL;
ALTER TABLE CommonAnnotation
    ADD CONSTRAINT CommonAnnotation_AnnotationKVID_fkey FOREIGN KEY (AnnotationKVID) REFERENCES AnnotationKV (ID);
CREATE UNIQUE INDEX CommonAnnotation_alertgroup_annkv_idx ON CommonAnnotation (AlertGroupID, AnnotationKVID);

ALTER TABLE AlertLabel ADD COLUMN LabelKVID INTEGER;
UPDATE AlertLabel al SET LabelKVID = lk.ID
FROM LabelKV lk
WHERE lk.LabelKey = al.label AND lk.Value = al.value;
ALTER TABLE AlertLabel DROP COLUMN label, DROP COLUMN value;
ALTER TABLE AlertLabel ALTER COLUMN LabelKVID SET NOT NULL;
ALTER TABLE AlertLabel
    ADD CONSTRAINT AlertLabel_LabelKVID_fkey FOREIGN KEY (LabelKVID) REFERENCES LabelKV (ID);
CREATE UNIQUE INDEX AlertLabel_alert_labelkv_idx ON AlertLabel (AlertID, LabelKVID);

ALTER TABLE AlertAnnotation ADD COLUMN AnnotationKVID INTEGER;
UPDATE AlertAnnotation aa SET AnnotationKVID = ak.ID
FROM AnnotationKV ak
WHERE ak.AnnotationKey = aa.annotation AND ak.Value = aa.value;
ALTER TABLE AlertAnnotation DROP COLUMN annotation, DROP COLUMN value;
ALTER TABLE AlertAnnotation ALTER COLUMN AnnotationKVID SET NOT NULL;
ALTER TABLE AlertAnnotation
    ADD CONSTRAINT AlertAnnotation_AnnotationKVID_fkey FOREIGN KEY (AnnotationKVID) REFERENCES AnnotationKV (ID);
CREATE UNIQUE INDEX AlertAnnotation_alert_annkv_idx ON AlertAnnotation (AlertID, AnnotationKVID);

UPDATE Model SET version = '0.2.0';
