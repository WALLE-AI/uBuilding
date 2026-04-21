import type { Conversation, Message, UploadedFile } from "@/types/chat";

const BASE = "";

export async function fetchConversations(): Promise<Conversation[]> {
  const res = await fetch(`${BASE}/api/conversations`);
  if (!res.ok) throw new Error("Failed to fetch conversations");
  return res.json();
}

export async function fetchMessages(conversationId: string): Promise<Message[]> {
  const res = await fetch(`${BASE}/api/conversations/${conversationId}`);
  if (!res.ok) throw new Error("Failed to fetch conversation");
  const data = await res.json();
  return data.messages ?? [];
}

export async function createConversation(title?: string): Promise<Conversation> {
  const res = await fetch(`${BASE}/api/conversations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title: title || "New Conversation" }),
  });
  if (!res.ok) throw new Error("Failed to create conversation");
  return res.json();
}

export async function deleteConversation(id: string): Promise<void> {
  await fetch(`${BASE}/api/conversations/${id}`, { method: "DELETE" });
}

export async function updateConversationTitle(id: string, title: string): Promise<void> {
  await fetch(`${BASE}/api/conversations/${id}/title`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
  });
}

export async function getWorkspace(): Promise<string> {
  const res = await fetch(`${BASE}/api/workspace`);
  if (!res.ok) return "";
  const text = await res.text();
  try {
    const data = JSON.parse(text);
    return data?.workspace ?? "";
  } catch {
    return "";
  }
}

export async function setWorkspace(path: string): Promise<string> {
  const res = await fetch(`${BASE}/api/workspace`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ workspace: path }),
  });
  const text = await res.text();
  let data: Record<string, string> = {};
  try {
    data = JSON.parse(text);
  } catch {
    throw new Error(
      res.ok
        ? "响应格式错误，请确认后端服务正常运行"
        : `请求失败 (${res.status})`
    );
  }
  if (!res.ok) throw new Error(data?.error ?? "设置工作空间失败");
  return data?.workspace ?? path;
}

export async function uploadFile(file: File): Promise<UploadedFile> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(`${BASE}/api/upload`, { method: "POST", body: form });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error((err as { error?: string }).error ?? "上传失败");
  }
  return res.json() as Promise<UploadedFile>;
}
