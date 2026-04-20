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
    <aside className="w-64 flex-shrink-0 flex flex-col bg-slate-50 border-r border-slate-200 h-full">
      {/* Brand header */}
      <div className="h-14 px-5 flex items-center justify-between border-b border-slate-200 shrink-0">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-indigo-600 flex items-center justify-center shadow-sm shadow-indigo-500/30">
            <MessageSquare size={14} className="text-white" />
          </div>
          <span className="font-semibold text-slate-800 text-sm">Agent Chat</span>
        </div>
      </div>

      {/* New conversation button */}
      <div className="px-3 pt-3 pb-2">
        <button
          onClick={onNew}
          className="w-full flex items-center justify-center gap-2 px-3 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 active:scale-[0.98] text-white text-sm font-medium transition-all shadow-sm shadow-indigo-500/20"
        >
          <Plus size={15} />
          新对话
        </button>
      </div>

      {/* Conversation list */}
      <div className="flex-1 overflow-y-auto px-2 pb-2">
        {conversations.length === 0 ? (
          <p className="text-slate-400 text-xs text-center mt-8 px-4 leading-relaxed">
            暂无对话记录<br />点击「新对话」开始
          </p>
        ) : (
          <div className="space-y-0.5 mt-1">
            {conversations.map((conv) => (
              <div
                key={conv.id}
                className={`group relative flex items-center gap-2.5 rounded-xl px-3 py-2.5 cursor-pointer transition-all ${
                  activeId === conv.id
                    ? "bg-white shadow-sm border border-slate-200 text-slate-800"
                    : "text-slate-500 hover:bg-white hover:text-slate-700 hover:shadow-sm hover:border hover:border-slate-200"
                }`}
                onClick={() => onSelect(conv.id)}
              >
                <MessageSquare
                  size={13}
                  className={`flex-shrink-0 ${activeId === conv.id ? "text-indigo-500" : "text-slate-400"}`}
                />

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
                      className="flex-1 bg-slate-100 border border-slate-300 text-slate-800 text-sm rounded-lg px-2 py-0.5 outline-none focus:border-indigo-300 min-w-0 transition-colors"
                    />
                    <button onClick={commitEdit} className="text-emerald-500 hover:text-emerald-600 transition-colors">
                      <Check size={12} />
                    </button>
                    <button onClick={cancelEdit} className="text-slate-400 hover:text-slate-600 transition-colors">
                      <X size={12} />
                    </button>
                  </div>
                ) : (
                  <>
                    <span className="flex-1 text-sm truncate">{conv.title}</span>
                    <div className="hidden group-hover:flex items-center gap-0.5">
                      <button
                        onClick={(e) => { e.stopPropagation(); startEdit(conv); }}
                        className="p-1 rounded-lg text-slate-400 hover:text-slate-600 hover:bg-slate-100 transition-colors"
                      >
                        <Edit2 size={11} />
                      </button>
                      <button
                        onClick={(e) => { e.stopPropagation(); onDelete(conv.id); }}
                        className="p-1 rounded-lg text-slate-400 hover:text-red-500 hover:bg-red-50 transition-colors"
                      >
                        <Trash2 size={11} />
                      </button>
                    </div>
                  </>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="px-4 py-3 border-t border-slate-200 shrink-0">
        <p className="text-xs text-slate-400 text-center">WALL-AI · uBuilding v0.1</p>
      </div>
    </aside>
  );
}
