import { Link } from "@tanstack/react-router";
import { Page, PageHeader, Panel } from "@/components/common/page";

export function NotInSlice({ title, what }: { title: string; what: string }) {
  return (
    <Page>
      <PageHeader title={title} />
      <Panel>
        <div className="flex max-w-prose flex-col gap-2">
          <p className="font-medium">Not in this slice yet</p>
          <p className="text-muted">{what}</p>
          <p className="text-xs text-faint">
            See the roadmap in <span className="font-mono">docs/roadmap.md</span>. Meanwhile you can{" "}
            <Link to="/catalog">request something from the catalog</Link> or{" "}
            <Link to="/requests">follow your requests</Link>.
          </p>
        </div>
      </Panel>
    </Page>
  );
}
