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

export interface WSServerMessage {
  type: "token" | "done" | "error" | "conversation_id" | "system"
      | "thinking_delta" | "tool_use" | "tool_result" | "todo_update" | "ask_question";
  content?: string;
  message_id?: string;
  conversation_id?: string;
  tool_id?: string;
  tool_name?: string;
  request_id?: string;
  options?: string[];
}

export interface WSClientMessage {
  type: "chat" | "new_conversation" | "question_reply";
  content?: string;
  conversation_id?: string;
  request_id?: string;
}

export type SendStatus = "idle" | "sending" | "streaming";

export interface TodoItem {
  id: string;
  content: string;
  activeForm?: string;
  status: "pending" | "in_progress" | "completed" | "failed";
  priority?: "high" | "medium" | "low";
}

export type StreamBlock =
  | { type: "text"; content: string }
  | { type: "thinking"; content: string; streaming?: boolean }
  | { type: "tool_use"; id: string; name: string; input?: string; status: "running" | "done"; result?: string }
  | { type: "todo"; id: string; todos: TodoItem[]; status: "running" | "done" }
  | { type: "ask_question"; requestId: string; question: string; options?: string[]; answered?: string };
