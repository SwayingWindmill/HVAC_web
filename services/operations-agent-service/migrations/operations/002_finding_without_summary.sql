\set ON_ERROR_STOP on

-- The finding card no longer repeats the answer's conclusion (#591): drop the sentence from the
-- findings stored before the change, so earlier Sessions read under the current contract.

BEGIN;

UPDATE agent_operations.agent_artifacts
SET artifact_payload = artifact_payload #- '{finding,summary}'
WHERE kind = 'FINDING' AND artifact_payload->'finding' ? 'summary';

COMMIT;
