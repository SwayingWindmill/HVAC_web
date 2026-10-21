// Which conversation the 助手 tab shows, kept while the mini program runs: the history page picks
// one and goes back, the tab reads the choice when it shows. null is a new conversation;
// undefined means nothing was chosen yet, and the tab opens the latest one still in progress.

let chosen: string | null | undefined;

export const chosenConversation = () => chosen;

export const chooseConversation = (sessionId: string | null) => {
  chosen = sessionId;
};
