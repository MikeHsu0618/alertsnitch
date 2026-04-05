-- Deduplicate label and annotation key/value pairs via lookup tables.
-- KvHash is MD5(hex) of key || ASCII SOH || value so unique indexes stay within engine limits.

CREATE TABLE `LabelKV` (
    `ID` INT NOT NULL AUTO_INCREMENT,
    `LabelKey` VARCHAR(100) NOT NULL,
    `Value` VARCHAR(1000) NOT NULL,
    `KvHash` CHAR(32) NOT NULL,
    PRIMARY KEY (`ID`),
    UNIQUE KEY `LabelKV_KvHash` (`KvHash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

CREATE TABLE `AnnotationKV` (
    `ID` INT NOT NULL AUTO_INCREMENT,
    `AnnotationKey` VARCHAR(100) NOT NULL,
    `Value` VARCHAR(1000) NOT NULL,
    `KvHash` CHAR(32) NOT NULL,
    PRIMARY KEY (`ID`),
    UNIQUE KEY `AnnotationKV_KvHash` (`KvHash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

INSERT IGNORE INTO `LabelKV` (`LabelKey`, `Value`, `KvHash`)
SELECT DISTINCT `GroupLabel`, `Value`, MD5(CONCAT(`GroupLabel`, CHAR(1), `Value`)) FROM `GroupLabel`;

INSERT IGNORE INTO `LabelKV` (`LabelKey`, `Value`, `KvHash`)
SELECT DISTINCT `Label`, `Value`, MD5(CONCAT(`Label`, CHAR(1), `Value`)) FROM `CommonLabel`;

INSERT IGNORE INTO `LabelKV` (`LabelKey`, `Value`, `KvHash`)
SELECT DISTINCT `Label`, `Value`, MD5(CONCAT(`Label`, CHAR(1), `Value`)) FROM `AlertLabel`;

INSERT IGNORE INTO `AnnotationKV` (`AnnotationKey`, `Value`, `KvHash`)
SELECT DISTINCT `Annotation`, `Value`, MD5(CONCAT(`Annotation`, CHAR(1), `Value`)) FROM `CommonAnnotation`;

INSERT IGNORE INTO `AnnotationKV` (`AnnotationKey`, `Value`, `KvHash`)
SELECT DISTINCT `Annotation`, `Value`, MD5(CONCAT(`Annotation`, CHAR(1), `Value`)) FROM `AlertAnnotation`;

ALTER TABLE `GroupLabel` ADD COLUMN `LabelKVID` INT NULL;
UPDATE `GroupLabel` gl
INNER JOIN `LabelKV` lk ON gl.`GroupLabel` = lk.`LabelKey` AND gl.`Value` = lk.`Value`
SET gl.`LabelKVID` = lk.`ID`;
ALTER TABLE `GroupLabel`
    DROP COLUMN `GroupLabel`,
    DROP COLUMN `Value`,
    MODIFY COLUMN `LabelKVID` INT NOT NULL,
    ADD CONSTRAINT `fk_grouplabel_labelkv` FOREIGN KEY (`LabelKVID`) REFERENCES `LabelKV` (`ID`) ON DELETE RESTRICT,
    ADD UNIQUE KEY `GroupLabel_alertgroup_labelkv` (`AlertGroupID`, `LabelKVID`);

ALTER TABLE `CommonLabel` ADD COLUMN `LabelKVID` INT NULL;
UPDATE `CommonLabel` cl
INNER JOIN `LabelKV` lk ON cl.`Label` = lk.`LabelKey` AND cl.`Value` = lk.`Value`
SET cl.`LabelKVID` = lk.`ID`;
ALTER TABLE `CommonLabel`
    DROP COLUMN `Label`,
    DROP COLUMN `Value`,
    MODIFY COLUMN `LabelKVID` INT NOT NULL,
    ADD CONSTRAINT `fk_commonlabel_labelkv` FOREIGN KEY (`LabelKVID`) REFERENCES `LabelKV` (`ID`) ON DELETE RESTRICT,
    ADD UNIQUE KEY `CommonLabel_alertgroup_labelkv` (`AlertGroupID`, `LabelKVID`);

ALTER TABLE `CommonAnnotation` ADD COLUMN `AnnotationKVID` INT NULL;
UPDATE `CommonAnnotation` ca
INNER JOIN `AnnotationKV` ak ON ca.`Annotation` = ak.`AnnotationKey` AND ca.`Value` = ak.`Value`
SET ca.`AnnotationKVID` = ak.`ID`;
ALTER TABLE `CommonAnnotation`
    DROP COLUMN `Annotation`,
    DROP COLUMN `Value`,
    MODIFY COLUMN `AnnotationKVID` INT NOT NULL,
    ADD CONSTRAINT `fk_commonannotation_annkv` FOREIGN KEY (`AnnotationKVID`) REFERENCES `AnnotationKV` (`ID`) ON DELETE RESTRICT,
    ADD UNIQUE KEY `CommonAnnotation_alertgroup_annkv` (`AlertGroupID`, `AnnotationKVID`);

ALTER TABLE `AlertLabel` ADD COLUMN `LabelKVID` INT NULL;
UPDATE `AlertLabel` al
INNER JOIN `LabelKV` lk ON al.`Label` = lk.`LabelKey` AND al.`Value` = lk.`Value`
SET al.`LabelKVID` = lk.`ID`;
ALTER TABLE `AlertLabel`
    DROP COLUMN `Label`,
    DROP COLUMN `Value`,
    MODIFY COLUMN `LabelKVID` INT NOT NULL,
    ADD CONSTRAINT `fk_alertlabel_labelkv` FOREIGN KEY (`LabelKVID`) REFERENCES `LabelKV` (`ID`) ON DELETE RESTRICT,
    ADD UNIQUE KEY `AlertLabel_alert_labelkv` (`AlertID`, `LabelKVID`);

ALTER TABLE `AlertAnnotation` ADD COLUMN `AnnotationKVID` INT NULL;
UPDATE `AlertAnnotation` aa
INNER JOIN `AnnotationKV` ak ON aa.`Annotation` = ak.`AnnotationKey` AND aa.`Value` = ak.`Value`
SET aa.`AnnotationKVID` = ak.`ID`;
ALTER TABLE `AlertAnnotation`
    DROP COLUMN `Annotation`,
    DROP COLUMN `Value`,
    MODIFY COLUMN `AnnotationKVID` INT NOT NULL,
    ADD CONSTRAINT `fk_alertannotation_annkv` FOREIGN KEY (`AnnotationKVID`) REFERENCES `AnnotationKV` (`ID`) ON DELETE RESTRICT,
    ADD UNIQUE KEY `AlertAnnotation_alert_annkv` (`AlertID`, `AnnotationKVID`);

UPDATE `Model` SET `version` = '0.2.0';
