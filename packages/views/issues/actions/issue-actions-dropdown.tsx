"use client";

import { useState, type ReactElement } from "react";
import type { Issue } from "@multica/core/types";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
} from "@multica/ui/components/ui/dropdown-menu";
import { useIssueActions, type UseIssueActionsResult } from "./use-issue-actions";
import {
  IssueActionsMenuItems,
  dropdownPrimitives,
} from "./issue-actions-menu-items";
import { AssigneePicker } from "../components/pickers";

interface IssueActionsDropdownProps {
  issue: Issue;
  /** A single React element cloned by Base UI as the trigger (via `render` prop). */
  trigger: ReactElement;
  align?: "start" | "end" | "center";
  /** If set, leave the page after the issue is deleted — back to wherever the
   *  user came from, or to this path when there is no in-app history. */
  onDeletedFallbackPath?: string;
  /** Override shared updates when a detail surface has an exact workflow snapshot. */
  onUpdateField?: UseIssueActionsResult["updateField"];
}

export function IssueActionsDropdown({
  issue,
  trigger,
  align = "end",
  onDeletedFallbackPath,
  onUpdateField,
}: IssueActionsDropdownProps) {
  const actions = useIssueActions(issue);
  const menuActions = onUpdateField ? { ...actions, updateField: onUpdateField } : actions;
  const [assigneeOpen, setAssigneeOpen] = useState(false);

  // The outer `relative inline-flex` is the picker's anchor box: the
  // absolute, pointer-events-none span inside `triggerRender` fills it, so
  // the popover positions itself relative to the dropdown's 3-dot button
  // without us having to thread a ref through Base UI's anchor API.
  return (
    <span className="relative inline-flex">
      <DropdownMenu>
        <DropdownMenuTrigger render={trigger} />
        <DropdownMenuContent align={align} className="w-auto">
          <IssueActionsMenuItems
            issue={issue}
            actions={menuActions}
            primitives={dropdownPrimitives}
            onOpenAssignee={() => setAssigneeOpen(true)}
            onDeletedFallbackPath={onDeletedFallbackPath}
          />
        </DropdownMenuContent>
      </DropdownMenu>
      {/* Mount the picker only once the user actually opens it. Otherwise
          every row in a list/board would subscribe to members/agents/squads
          /frequency queries on mount, multiplying memory + render cost. */}
      {assigneeOpen && (
        <AssigneePicker
          assigneeType={issue.assignee_type}
          assigneeId={issue.assignee_id}
          onUpdate={menuActions.updateField}
          open={assigneeOpen}
          onOpenChange={setAssigneeOpen}
          triggerRender={
            <span
              aria-hidden
              className="pointer-events-none absolute inset-0"
            />
          }
          trigger={<span />}
          align={align}
        />
      )}
    </span>
  );
}
