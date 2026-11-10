// Client-side routes for the admin area. The server serves the same shell for every
// path and authorises every API call; a route here only chooses which screen to draw.

export type AdminRoute = { kind: "users" } | { kind: "user"; id: string } | { kind: "posts" } | { kind: "post"; id: string } | { kind: "audit" } | { kind: "capabilities" } | { kind: "usage" } | { kind: "unknown" };

export const isAdminPath = (p: string): boolean => p === "/admin" || p.startsWith("/admin/");

export function parseAdminPath(p: string): AdminRoute {
  const path = p.replace(/\/+$/, "") || "/";
  if (path === "/admin" || path === "/admin/users") return { kind: "users" };
  if (path === "/admin/audit") return { kind: "audit" };
  if (path === "/admin/posts") return { kind: "posts" };
  const post = /^\/admin\/posts\/([0-9a-f]{32})$/.exec(path);
  if (post) return { kind: "post", id: post[1] };
  if (path === "/admin/capabilities") return { kind: "capabilities" };
  if (path === "/admin/usage") return { kind: "usage" };
  const m = /^\/admin\/users\/([0-9a-f]{32})$/.exec(path);
  return m ? { kind: "user", id: m[1] } : { kind: "unknown" };
}

export const userPath = (id: string) => `/admin/users/${id}`;
export const adminPostPath = (id: string) => `/admin/posts/${id}`;
