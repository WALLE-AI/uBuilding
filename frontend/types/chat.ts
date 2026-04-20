export interface Conversation {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface Message {
  id: string;
  conversation_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at: string;
}

export interface WSClientMessage {
  type: "chat" | "new_conversation";
  content?: string;
  conversation_id?: string;
}

export interface WSServerMessage {
  type: "token" | "done" | "error" | "conversation_id" | "system"
      | "thinking_delta" | "tool_use" | "tool_result";
  content?: string;
  message_id?: string;
  conversation_id?: string;
  tool_id?: string;
  tool_name?: string;
}

export type SendStatus = "idle" | "sending" | "streaming";

export type StreamBlock =
  | { type: "text"; content: string }
  | { type: "thinking"; content: string; streaming?: boolean }
  | { type: "tool_use"; id: string; name: string; input?: string; status: "running" | "done"; result?: string };
