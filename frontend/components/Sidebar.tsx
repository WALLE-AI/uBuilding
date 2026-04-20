"use client";

import { useState } from "react";
import { MessageSquare, Plus, Trash2, Edit2, Check, X } from "lucide-react";
import type { Conversation } from "@/types/chat";

interface SidebarProps {
  conversations: Conversation[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  onDelete: (id: string) => void;
  onRename: (id: string, title: string) => void;
}

export default function Sidebar({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  onRename,
}: SidebarProps) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editTitle, setEditTitle] = useState("");

  const startEdit = (conv: Conversation) => {
    setEditingId(conv.id);
    setEditTitle(conv.title);
  };

  const commitEdit = () => {
    if (editingId && editTitle.trim()) {
      onRename(editingId, editTitle.trim());
    }
    setEditingId(null);
  };

  const cancelEdit = () => setEditingId(null);

  return (
    <aside className="w-64 flex-shrink-0 flex flex-col bg-gray-900 border-r border-gray-800 h-full">
      <div className="p-4 border-b border-gray-800">
        <button
          onClick={onNew}
          className="w-full flex items-center gap-2 px-3 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
        >
          <Plus size={16} />
          新对话
        </button>
      </div>

      <div className="flex-1 overflow-y-auto py-2">
        {conversations.length === 0 && (
          <p className="text-gray-500 text-xs text-center mt-8 px-4">
            暂无对话记录
          </p>
        )}
        {conversations.map((conv) => (
          <div
            key={conv.id}
            className={`group relative flex items-center gap-2 mx-2 mb-1 rounded-lg px-3 py-2.5 cursor-pointer transition-colors ${
              activeId === conv.id
                ? "bg-gray-700 text-white"
                : "text-gray-400 hover:bg-gray-800 hover:text-gray-200"
            }`}
            onClick={() => onSelect(conv.id)}
          >
            <MessageSquare size={14} className="flex-shrink-0" />

            {editingId === conv.id ? (
              <div className="flex-1 flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                <input
                  autoFocus
                  value={editTitle}
                  onChange={(e) => setEditTitle(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") commitEdit();
                    if (e.key === "Escape") cancelEdit();
                  }}
                  className="flex-1 bg-gray-600 text-white text-sm rounded px-1 py-0.5 outline-none min-w-0"
                />
                <button onClick={commitEdit} className="text-green-400 hover:text-green-300">
                  <Check size={12} />
                </button>
                <button onClick={cancelEdit} className="text-gray-400 hover:text-gray-300">
                  <X size={12} />
                </button>
              </div>
            ) : (
              <>
                <span className="flex-1 text-sm truncate">{conv.title}</span>
                <div className="hidden group-hover:flex items-center gap-1">
                  <button
                    onClick={(e) => { e.stopPropagation(); startEdit(conv); }}
                    className="p-0.5 rounded hover:text-white"
                  >
                    <Edit2 size={12} />
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); onDelete(conv.id); }}
                    className="p-0.5 rounded hover:text-red-400"
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              </>
            )}
          </div>
        ))}
      </div>

      <div className="p-3 border-t border-gray-800">
        <p className="text-xs text-gray-600 text-center">Agent Chat v0.1</p>
      </div>
    </aside>
  );
}
