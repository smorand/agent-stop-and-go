package main

import "fmt"

// chatHTML returns the embedded HTML chat interface.
// hasOAuth controls whether the login/logout buttons are rendered.
func chatHTML(agentURL string, hasOAuth bool) string {
	oauthJS := ""
	logoutButton := ""
	if hasOAuth {
		logoutButton = `<button class="logout-btn" id="logoutBtn" style="display:none" onclick="doLogout()">Logout</button>`
		oauthJS = `
    // Check session status on load
    async function checkSession() {
        try {
            const resp = await fetch('/api/session');
            const data = await resp.json();
            const btn = document.getElementById('logoutBtn');
            if (btn) btn.style.display = data.authenticated ? 'inline-block' : 'none';
        } catch (e) {}
    }
    checkSession();

    function doLogout() {
        const form = document.createElement('form');
        form.method = 'POST';
        form.action = '/logout';
        document.body.appendChild(form);
        form.submit();
    }

    // Handle auth_required: store pending message and redirect to /login
    function handleAuthRequired(message, conversationId) {
        sessionStorage.setItem('pending_auth_message', JSON.stringify({
            message: message,
            conversation_id: conversationId || ''
        }));
        window.location.href = '/login';
    }

    // On page load, check for pending auth message (auto-retry after OAuth2 flow)
    async function checkPendingAuthMessage() {
        const pending = sessionStorage.getItem('pending_auth_message');
        if (!pending) return;

        sessionStorage.removeItem('pending_auth_message');
        const data = JSON.parse(pending);

        addMessage('system', 'Authentication completed. Retrying your request...');
        addMessage('user', data.message);
        showTyping();

        sending = true;
        sendBtn.disabled = true;

        try {
            const resp = await fetch('/api/send', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-Auth-Retry': '1'
                },
                body: JSON.stringify({ message: data.message, conversation_id: data.conversation_id })
            });

            hideTyping();

            if (!resp.ok) {
                const err = await resp.json();
                addMessage('error', 'Error: ' + (err.error || resp.statusText));
                return;
            }

            const result = await resp.json();

            // Infinite loop detection: if auth_required again after retry, show error
            if (result.result && result.result.auth_required) {
                addMessage('error', 'Authentication failed. The server rejected your credentials. Please check that you are using the correct Google account and that the required permissions are granted.');
                return;
            }

            handleResult(result);
        } catch (err) {
            hideTyping();
            addMessage('error', 'Network error: ' + err.message);
        } finally {
            sending = false;
            sendBtn.disabled = false;
            input.focus();
        }
    }
    checkPendingAuthMessage();
`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Agent Stop and Go - Chat</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; background: #1a1a2e; color: #eee; height: 100vh; display: flex; flex-direction: column; }

        header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 15px 20px; display: flex; align-items: center; gap: 15px; }
        header h1 { font-size: 1.4em; }
        header .status { font-size: 0.85em; opacity: 0.8; margin-left: auto; }
        header .new-chat { background: rgba(255,255,255,0.2); color: #fff; border: 1px solid rgba(255,255,255,0.3); padding: 6px 14px; border-radius: 6px; cursor: pointer; font-size: 0.85em; font-weight: bold; }
        header .new-chat:hover { background: rgba(255,255,255,0.3); }
        header .logout-btn { background: rgba(255,100,100,0.3); color: #fff; border: 1px solid rgba(255,100,100,0.4); padding: 6px 14px; border-radius: 6px; cursor: pointer; font-size: 0.85em; font-weight: bold; }
        header .logout-btn:hover { background: rgba(255,100,100,0.5); }

        .chat-container { flex: 1; overflow-y: auto; padding: 20px; display: flex; flex-direction: column; gap: 12px; }

        .message { max-width: 80%%; padding: 12px 16px; border-radius: 12px; line-height: 1.5; word-wrap: break-word; white-space: pre-wrap; }
        .message.user { align-self: flex-end; background: #667eea; color: #fff; border-bottom-right-radius: 4px; }
        .message.assistant { align-self: flex-start; background: #252540; border: 1px solid #3a3a5c; border-bottom-left-radius: 4px; }
        .message.system { align-self: center; background: #1e1e3f; color: #888; font-size: 0.85em; border-radius: 8px; max-width: 90%%; text-align: center; }
        .message.error { align-self: center; background: #3e1e1e; color: #f93e3e; border: 1px solid #5e2e2e; }

        .approval-box { align-self: flex-start; background: #2a2a1e; border: 1px solid #fca130; border-radius: 12px; padding: 16px; max-width: 80%%; }
        .approval-box .label { color: #fca130; font-weight: bold; margin-bottom: 8px; font-size: 0.9em; text-transform: uppercase; }
        .approval-box .desc { margin-bottom: 12px; white-space: pre-wrap; line-height: 1.5; }
        .approval-box .actions { display: flex; gap: 10px; }
        .approval-box button { padding: 8px 20px; border: none; border-radius: 6px; cursor: pointer; font-weight: bold; font-size: 0.9em; }
        .approval-box .approve-btn { background: #49cc90; color: #fff; }
        .approval-box .approve-btn:hover { background: #3cb87e; }
        .approval-box .reject-btn { background: #f93e3e; color: #fff; }
        .approval-box .reject-btn:hover { background: #e03030; }
        .approval-box button:disabled { opacity: 0.5; cursor: not-allowed; }

        .input-area { padding: 15px 20px; background: #252540; border-top: 1px solid #3a3a5c; display: flex; gap: 10px; }
        .input-area input { flex: 1; padding: 12px 16px; border: 1px solid #3a3a5c; border-radius: 8px; background: #1a1a2e; color: #eee; font-size: 1em; outline: none; }
        .input-area input:focus { border-color: #667eea; }
        .input-area button { padding: 12px 24px; background: #667eea; color: #fff; border: none; border-radius: 8px; cursor: pointer; font-weight: bold; font-size: 1em; }
        .input-area button:hover { background: #5a6fd6; }
        .input-area button:disabled { opacity: 0.5; cursor: not-allowed; }

        .typing { align-self: flex-start; color: #888; font-style: italic; padding: 8px 16px; }
        .typing::after { content: '...'; animation: dots 1.5s infinite; }
        @keyframes dots { 0%%, 20%% { content: '.'; } 40%% { content: '..'; } 60%%, 100%% { content: '...'; } }

        code { background: #1e1e3f; padding: 2px 6px; border-radius: 4px; font-family: 'Monaco', 'Menlo', monospace; font-size: 0.9em; }
        pre { background: #1e1e3f; padding: 12px; border-radius: 8px; overflow-x: auto; margin: 8px 0; }
        pre code { background: none; padding: 0; }
    </style>
</head>
<body>
    <header>
        <h1>Agent Stop and Go</h1>
        <button class="new-chat" onclick="newChat()">New Chat</button>
        %s
        <span class="status">Connected to %s</span>
    </header>

    <div class="chat-container" id="chat">
        <div class="message system">Send a message to start a conversation with the agent.</div>
    </div>

    <div class="input-area">
        <input type="text" id="messageInput" placeholder="Type a message..." autofocus />
        <button id="sendBtn" onclick="sendMessage()">Send</button>
    </div>

    <script>
    let currentConversationId = null;
    let sending = false;
    let lastSentMessage = '';

    const chat = document.getElementById('chat');
    const input = document.getElementById('messageInput');
    const sendBtn = document.getElementById('sendBtn');

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !sending) sendMessage();
    });

    function newChat() {
        currentConversationId = null;
        chat.innerHTML = '<div class="message system">Send a message to start a conversation with the agent.</div>';
        input.focus();
    }

    function addMessage(role, text) {
        const div = document.createElement('div');
        div.className = 'message ' + role;
        div.textContent = text;
        chat.appendChild(div);
        chat.scrollTop = chat.scrollHeight;
    }

    function addApproval(uuid, description) {
        const box = document.createElement('div');
        box.className = 'approval-box';
        box.id = 'approval-' + uuid;
        box.innerHTML =
            '<div class="label">Approval Required</div>' +
            '<div class="desc">' + escapeHtml(description) + '</div>' +
            '<div class="actions">' +
            '<button class="approve-btn" onclick="approveAction(\'' + uuid + '\', true)">Approve</button>' +
            '<button class="reject-btn" onclick="approveAction(\'' + uuid + '\', false)">Reject</button>' +
            '</div>';
        chat.appendChild(box);
        chat.scrollTop = chat.scrollHeight;
    }

    function showTyping() {
        const div = document.createElement('div');
        div.className = 'typing';
        div.id = 'typing';
        div.textContent = 'Agent is thinking';
        chat.appendChild(div);
        chat.scrollTop = chat.scrollHeight;
    }

    function hideTyping() {
        const el = document.getElementById('typing');
        if (el) el.remove();
    }

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function handleResult(data) {
        if (data.conversation && data.conversation.id) {
            currentConversationId = data.conversation.id;
        }

        if (data.result) {
            // Handle auth_required
            if (data.result.auth_required && typeof handleAuthRequired === 'function') {
                handleAuthRequired(lastSentMessage, currentConversationId);
                return;
            }

            if (data.result.waiting_approval && data.result.approval) {
                const desc = data.result.approval.description || 'An action requires your approval.';
                addApproval(data.result.approval.uuid, desc);
            } else if (data.result.response) {
                addMessage('assistant', data.result.response);
            }
        }
    }

    async function sendMessage() {
        const message = input.value.trim();
        if (!message || sending) return;

        sending = true;
        sendBtn.disabled = true;
        input.value = '';
        lastSentMessage = message;

        addMessage('user', message);
        showTyping();

        try {
            const resp = await fetch('/api/send', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ message, conversation_id: currentConversationId || '' })
            });

            hideTyping();

            if (!resp.ok) {
                const err = await resp.json();
                addMessage('error', 'Error: ' + (err.error || resp.statusText));
                return;
            }

            const data = await resp.json();
            handleResult(data);
        } catch (err) {
            hideTyping();
            addMessage('error', 'Network error: ' + err.message);
        } finally {
            sending = false;
            sendBtn.disabled = false;
            input.focus();
        }
    }

    async function approveAction(uuid, approved) {
        const box = document.getElementById('approval-' + uuid);
        if (box) {
            const buttons = box.querySelectorAll('button');
            buttons.forEach(b => b.disabled = true);
        }

        const action = approved ? 'Approved' : 'Rejected';
        addMessage('system', action + ' by user.');
        showTyping();

        try {
            const resp = await fetch('/api/approve', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ uuid, approved })
            });

            hideTyping();

            if (!resp.ok) {
                const err = await resp.json();
                addMessage('error', 'Approval error: ' + (err.error || resp.statusText));
                return;
            }

            const data = await resp.json();
            handleResult(data);
        } catch (err) {
            hideTyping();
            addMessage('error', 'Network error: ' + err.message);
        }
    }

    %s
    </script>
</body>
</html>`, logoutButton, agentURL, oauthJS)
}
