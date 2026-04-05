-- Deduplicate AlertGroup receiver, externalURL, and groupKey via lookup tables.

CREATE TABLE `AlertGroupReceiver` (
    `ID` INT NOT NULL AUTO_INCREMENT,
    `Receiver` VARCHAR(100) NOT NULL,
    PRIMARY KEY (`ID`),
    UNIQUE KEY `AlertGroupReceiver_Receiver` (`Receiver`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- VARCHAR(1000) utf8 keeps the unique index within InnoDB's key length limit (TEXT cannot be fully unique in MySQL).
CREATE TABLE `AlertGroupExternalURL` (
    `ID` INT NOT NULL AUTO_INCREMENT,
    `ExternalURL` VARCHAR(1000) CHARACTER SET utf8 NOT NULL,
    PRIMARY KEY (`ID`),
    UNIQUE KEY `AlertGroupExternalURL_ExternalURL` (`ExternalURL`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

CREATE TABLE `AlertGroupKey` (
    `ID` INT NOT NULL AUTO_INCREMENT,
    `GroupKey` VARCHAR(255) NOT NULL,
    PRIMARY KEY (`ID`),
    UNIQUE KEY `AlertGroupKey_GroupKey` (`GroupKey`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

INSERT IGNORE INTO `AlertGroupReceiver` (`Receiver`)
SELECT DISTINCT `receiver` FROM `AlertGroup`;

INSERT IGNORE INTO `AlertGroupExternalURL` (`ExternalURL`)
SELECT DISTINCT `externalURL` FROM `AlertGroup`;

INSERT IGNORE INTO `AlertGroupKey` (`GroupKey`)
SELECT DISTINCT `groupKey` FROM `AlertGroup`;

ALTER TABLE `AlertGroup` ADD COLUMN `ReceiverID` INT NULL;
ALTER TABLE `AlertGroup` ADD COLUMN `ExternalURLID` INT NULL;
ALTER TABLE `AlertGroup` ADD COLUMN `KeyID` INT NULL;

UPDATE `AlertGroup` ag
INNER JOIN `AlertGroupReceiver` rk ON ag.`receiver` = rk.`Receiver`
SET ag.`ReceiverID` = rk.`ID`;

UPDATE `AlertGroup` ag
INNER JOIN `AlertGroupExternalURL` ek ON ag.`externalURL` = ek.`ExternalURL`
SET ag.`ExternalURLID` = ek.`ID`;

UPDATE `AlertGroup` ag
INNER JOIN `AlertGroupKey` gk ON ag.`groupKey` = gk.`GroupKey`
SET ag.`KeyID` = gk.`ID`;

ALTER TABLE `AlertGroup`
    DROP COLUMN `receiver`,
    DROP COLUMN `externalURL`,
    DROP COLUMN `groupKey`,
    MODIFY COLUMN `ReceiverID` INT NOT NULL,
    MODIFY COLUMN `ExternalURLID` INT NOT NULL,
    MODIFY COLUMN `KeyID` INT NOT NULL,
    ADD CONSTRAINT `fk_alertgroup_AlertGroupReceiver` FOREIGN KEY (`ReceiverID`) REFERENCES `AlertGroupReceiver` (`ID`) ON DELETE RESTRICT,
    ADD CONSTRAINT `fk_alertgroup_AlertGroupExternalURL` FOREIGN KEY (`ExternalURLID`) REFERENCES `AlertGroupExternalURL` (`ID`) ON DELETE RESTRICT,
    ADD CONSTRAINT `fk_alertgroup_AlertGroupKey` FOREIGN KEY (`KeyID`) REFERENCES `AlertGroupKey` (`ID`) ON DELETE RESTRICT;

UPDATE `Model` SET `version` = '0.3.0';
