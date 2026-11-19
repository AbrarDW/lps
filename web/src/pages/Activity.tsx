import { useState } from "react";
import type { ActivitySubject } from "../api";
import { ActivityList } from "../components/Activity";
import { Card, PageHeader, Tabs } from "../components/ui";

const filters: { id: ActivitySubject | "all"; label: string }[] = [
  { id: "all", label: "Everything" },
  { id: "vps", label: "VPS" },
  { id: "app", label: "Apps" },
  { id: "remote", label: "Remote access" },
  { id: "auth", label: "Sign-ins" },
];

export function ActivityPage() {
  const [filter, setFilter] = useState<ActivitySubject | "all">("all");
  return (
    <>
      <PageHeader
        sheet="Sheet 05 — Activity"
        title="Activity"
        subtitle="What happened on this server, who did it and from where. Deploys, restarts and failures LPS handles by itself show as “by LPS”."
      />
      <Card flush marks>
        <Tabs label="Show" tabs={filters} value={filter} onChange={setFilter} />
        <ActivityList key={filter} subject={filter === "all" ? "" : filter} limit={50} paged />
      </Card>
    </>
  );
}
