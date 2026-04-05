-- Deduplicate AlertGroup receiver, externalURL, and groupKey via lookup tables.

CREATE TABLE AlertGroupReceiver (
    ID SERIAL NOT NULL PRIMARY KEY,
    Receiver VARCHAR(100) NOT NULL,
    CONSTRAINT AlertGroupReceiver_Receiver_key UNIQUE (Receiver)
);

CREATE TABLE AlertGroupExternalURL (
    ID SERIAL NOT NULL PRIMARY KEY,
    ExternalURL TEXT NOT NULL,
    CONSTRAINT AlertGroupExternalURL_ExternalURL_key UNIQUE (ExternalURL)
);

CREATE TABLE AlertGroupKey (
    ID SERIAL NOT NULL PRIMARY KEY,
    GroupKey VARCHAR(255) NOT NULL,
    CONSTRAINT AlertGroupKey_GroupKey_key UNIQUE (GroupKey)
);

INSERT INTO AlertGroupReceiver (Receiver)
SELECT DISTINCT receiver
FROM AlertGroup
ON CONFLICT (Receiver) DO NOTHING;

INSERT INTO AlertGroupExternalURL (ExternalURL)
SELECT DISTINCT externalurl
FROM AlertGroup
ON CONFLICT (ExternalURL) DO NOTHING;

INSERT INTO AlertGroupKey (GroupKey)
SELECT DISTINCT groupkey
FROM AlertGroup
ON CONFLICT (GroupKey) DO NOTHING;

ALTER TABLE AlertGroup ADD COLUMN ReceiverID INTEGER;
ALTER TABLE AlertGroup ADD COLUMN ExternalURLID INTEGER;
ALTER TABLE AlertGroup ADD COLUMN KeyID INTEGER;

UPDATE AlertGroup ag SET ReceiverID = rk.ID
FROM AlertGroupReceiver rk
WHERE ag.receiver = rk.Receiver;

UPDATE AlertGroup ag SET ExternalURLID = ek.ID
FROM AlertGroupExternalURL ek
WHERE ag.externalurl = ek.ExternalURL;

UPDATE AlertGroup ag SET KeyID = gk.ID
FROM AlertGroupKey gk
WHERE ag.groupkey = gk.GroupKey;

ALTER TABLE AlertGroup DROP COLUMN receiver, DROP COLUMN externalurl, DROP COLUMN groupkey;
ALTER TABLE AlertGroup ALTER COLUMN ReceiverID SET NOT NULL;
ALTER TABLE AlertGroup ALTER COLUMN ExternalURLID SET NOT NULL;
ALTER TABLE AlertGroup ALTER COLUMN KeyID SET NOT NULL;
ALTER TABLE AlertGroup
    ADD CONSTRAINT AlertGroup_ReceiverID_fkey FOREIGN KEY (ReceiverID) REFERENCES AlertGroupReceiver (ID),
    ADD CONSTRAINT AlertGroup_ExternalURLID_fkey FOREIGN KEY (ExternalURLID) REFERENCES AlertGroupExternalURL (ID),
    ADD CONSTRAINT AlertGroup_KeyID_fkey FOREIGN KEY (KeyID) REFERENCES AlertGroupKey (ID);

UPDATE Model SET version = '0.3.0';
