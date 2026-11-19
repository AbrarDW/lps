-- Earlier successful deployments stayed "running" after a redeploy. Only the
-- newest successful deployment of each app is live; mark the rest replaced.
UPDATE deployments SET status = 'replaced'
WHERE status = 'running'
  AND id NOT IN (SELECT MAX(id) FROM deployments WHERE status = 'running' GROUP BY app_id);
