import type { Conversation, Message } from "@/types/chat";

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
