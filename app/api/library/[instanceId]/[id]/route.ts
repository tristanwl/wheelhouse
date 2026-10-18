import { NextResponse } from "next/server";
import { resolveManager } from "@/lib/manager-route";

export async function DELETE(
  _req: Request,
  { params }: { params: Promise<{ instanceId: string; id: string }> }
) {
  const { instanceId, id: idParam } = await params;
  const id = Number(idParam);
  if (!Number.isInteger(id)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }
  const resolved = await resolveManager(instanceId);
  if ("response" in resolved) return resolved.response;

  try {
    await resolved.client.remove(id);
    return NextResponse.json({ ok: true });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "delete failed" },
      { status: 502 }
    );
  }
}
