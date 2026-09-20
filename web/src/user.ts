let currentUserId: number | null = null;

export function setCurrentUser(id: number | null): void {
  currentUserId = id;
}

export function currentUser(): number | null {
  return currentUserId;
}
