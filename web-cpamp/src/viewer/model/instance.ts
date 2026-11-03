import type { ViewerInstanceIdentity } from "./viewerTypes";

/** Instance names are public labels, never credential identities. */
export const withViewerInstance = (
  label: string,
  instance: ViewerInstanceIdentity,
) => {
  const name = instance.instance_name?.trim();
  return name ? `${label} · ${name}` : label;
};

export const viewerSourceWarnings = (
  ...responses: Array<{ warnings?: string[] } | null | undefined>
) =>
  Array.from(
    new Set(responses.flatMap((response) => response?.warnings ?? [])),
  );
