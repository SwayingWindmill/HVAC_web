// Which conversation the 助手 tab shows, kept while the mini program runs: the history page picks
// one and goes back, the tab reads the choice when it shows. null is a new conversation.

let chosen: string | null = null;

export const chosenConversation = () => chosen;

export const chooseConversation = (sessionId: string | null) => {
  chosen = sessionId;
};
