(() => {
  const button = document.getElementById('say-hello');
  const greeting = document.getElementById('greeting');
  if (!button || !greeting) return;

  button.addEventListener('click', () => {
    greeting.hidden = false;
  });
})();
