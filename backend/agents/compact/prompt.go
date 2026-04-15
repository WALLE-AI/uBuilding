package compact

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Compact Prompt Templates
// Maps to TypeScript services/compact/prompt.ts
//
// These prompts instruct the LLM to produce a structured conversation summary
// during auto-compact or slash-command compact.
// ---------------------------------------------------------------------------

// noToolsPreamble prevents the model from calling tools during compaction.
const noToolsPreamble = `CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.

- Do NOT use Read, Bash, Grep, Glob, Edit, Write, or ANY other tool.
- You already have all the context you need in the conversation above.
- Tool calls will be REJECTED and will waste your only turn — you will fail the task.
- Your entire response must be plain text: an <analysis> block followed by a <summary> block.

`

const detailedAnalysisBase = `Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points. In your analysis process:

1. Chronologically analyze each message and section of the conversation. For each section thoroughly identify:
   - The user's explicit requests and intents
   - Your approach to addressing the user's requests
   - Key decisions, technical concepts and code patterns
   - Specific details like:
     - file names
     - full code snippets
     - function signatures
     - file edits
   - Errors that you ran into and how you fixed them
   - Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
2. Double-check for technical accuracy and completeness, addressing each required element thoroughly.`

const detailedAnalysisPartial = `Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts and ensure you've covered all necessary points. In your analysis process:

1. Analyze the recent messages chronologically. For each section thoroughly identify:
   - The user's explicit requests and intents
   - Your approach to addressing the user's requests
   - Key decisions, technical concepts and code patterns
   - Specific details like:
     - file names
     - full code snippets
     - function signatures
     - file edits
   - Errors that you ran into and how you fixed them
   - Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
2. Double-check for technical accuracy and completeness, addressing each required element thoroughly.`

const baseCompactPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.
This summary should be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

` + detailedAnalysisBase + `

Your summary should include the following sections:

1. Primary Request and Intent: Capture all of the user's explicit requests and intents in detail
2. Key Technical Concepts: List all important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Pay special attention to the most recent messages and include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List all errors that you ran into, and how you fixed them. Pay special attention to specific user feedback that you received, especially if the user told you to do something differently.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results. These are critical for understanding the users' feedback and changing intent.
7. Pending Tasks: Outline any pending tasks that you have explicitly been asked to work on.
8. Current Work: Describe in detail precisely what was being worked on immediately before this summary request, paying special attention to the most recent messages from both user and assistant. Include file names and code snippets where applicable.
9. Optional Next Step: List the next step that you will take that is related to the most recent work you were doing. IMPORTANT: ensure that this step is DIRECTLY in line with the user's most recent explicit requests, and the task you were working on immediately before this summary request. If your last task was concluded, then only list next steps if they are explicitly in line with the users request. Do not start on tangential requests or really old requests that were already completed without confirming with the user first.
                       If there is a next step, include direct quotes from the most recent conversation showing exactly what task you were working on and where you left off. This should be verbatim to ensure there's no drift in task interpretation.

Please provide your summary based on the conversation so far, following this structure and ensuring precision and thoroughness in your response. 

There may be additional summarization instructions provided in the included context. If so, remember to follow these instructions when creating the above summary.`

const partialCompactPrompt = `Your task is to create a detailed summary of the RECENT portion of the conversation — the messages that follow earlier retained context. The earlier messages are being kept intact and do NOT need to be summarized. Focus your summary on what was discussed, learned, and accomplished in the recent messages only.

` + detailedAnalysisPartial + `

Your summary should include the following sections:

1. Primary Request and Intent: Capture the user's explicit requests and intents from the recent messages
2. Key Technical Concepts: List important technical concepts, technologies, and frameworks discussed recently.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List errors encountered and how they were fixed.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages from the recent portion that are not tool results.
7. Pending Tasks: Outline any pending tasks from the recent messages.
8. Current Work: Describe precisely what was being worked on immediately before this summary request.
9. Optional Next Step: List the next step related to the most recent work. Include direct quotes from the most recent conversation.

Please provide your summary based on the RECENT messages only (after the retained earlier context), following this structure and ensuring precision and thoroughness in your response.`

const partialCompactUpToPrompt = `Your task is to create a detailed summary of this conversation. This summary will be placed at the start of a continuing session; newer messages that build on this context will follow after your summary (you do not see them here). Summarize thoroughly so that someone reading only your summary and then the newer messages can fully understand what happened and continue the work.

` + detailedAnalysisBase + `

Your summary should include the following sections:

1. Primary Request and Intent: Capture the user's explicit requests and intents in detail
2. Key Technical Concepts: List important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include full code snippets where applicable and include a summary of why this file read or edit is important.
4. Errors and fixes: List errors encountered and how they were fixed.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results.
7. Pending Tasks: Outline any pending tasks.
8. Work Completed: Describe what was accomplished by the end of this portion.
9. Context for Continuing Work: Summarize any context, decisions, or state that would be needed to understand and continue the work in subsequent messages.

Please provide your summary following this structure, ensuring precision and thoroughness in your response.`

const noToolsTrailer = "\n\nREMINDER: Do NOT call any tools. Respond with plain text only — " +
	"an <analysis> block followed by a <summary> block. " +
	"Tool calls will be rejected and you will fail the task."

// PartialCompactDirection controls which messages are summarized.
type PartialCompactDirection string

const (
	PartialDirectionFrom  PartialCompactDirection = "from"
	PartialDirectionUpTo  PartialCompactDirection = "up_to"
)

// GetCompactPrompt returns the full compact prompt for summarizing the entire conversation.
func GetCompactPrompt(customInstructions string) string {
	prompt := noToolsPreamble + baseCompactPrompt

	if strings.TrimSpace(customInstructions) != "" {
		prompt += fmt.Sprintf("\n\nAdditional Instructions:\n%s", customInstructions)
	}

	prompt += noToolsTrailer
	return prompt
}

// GetPartialCompactPrompt returns the prompt for partial compaction.
func GetPartialCompactPrompt(customInstructions string, direction PartialCompactDirection) string {
	template := partialCompactPrompt
	if direction == PartialDirectionUpTo {
		template = partialCompactUpToPrompt
	}

	prompt := noToolsPreamble + template

	if strings.TrimSpace(customInstructions) != "" {
		prompt += fmt.Sprintf("\n\nAdditional Instructions:\n%s", customInstructions)
	}

	prompt += noToolsTrailer
	return prompt
}

// ---------------------------------------------------------------------------
// Summary formatting
// ---------------------------------------------------------------------------

var analysisRe = regexp.MustCompile(`(?s)<analysis>.*?</analysis>`)
var summaryRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
var multiNewlineRe = regexp.MustCompile(`\n{3,}`)

// FormatCompactSummary strips the <analysis> drafting block and formats the
// <summary> tags into readable headers.
// Maps to TypeScript formatCompactSummary.
func FormatCompactSummary(summary string) string {
	result := summary

	// Strip analysis section
	result = analysisRe.ReplaceAllString(result, "")

	// Extract and format summary section
	if match := summaryRe.FindStringSubmatch(result); len(match) > 1 {
		content := strings.TrimSpace(match[1])
		result = summaryRe.ReplaceAllString(result, "Summary:\n"+content)
	}

	// Clean up extra whitespace
	result = multiNewlineRe.ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result)
}

// GetCompactUserSummaryMessage builds the user-facing summary message
// that replaces compacted conversation history.
// Maps to TypeScript getCompactUserSummaryMessage.
func GetCompactUserSummaryMessage(
	summary string,
	suppressFollowUp bool,
	transcriptPath string,
	recentMessagesPreserved bool,
) string {
	formatted := FormatCompactSummary(summary)

	base := fmt.Sprintf(`This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.

%s`, formatted)

	if transcriptPath != "" {
		base += fmt.Sprintf("\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: %s", transcriptPath)
	}

	if recentMessagesPreserved {
		base += "\n\nRecent messages are preserved verbatim."
	}

	if suppressFollowUp {
		base += "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."
	}

	return base
}
