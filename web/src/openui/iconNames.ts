// The lucide icons a reply can render through OpenUI's Icon, IconButton, IconText and OptionCard.
// The library resolves a kebab-case name against lucide's whole catalogue (~1700 icons, one chunk
// each); the build (vite.config.ts, virtual module "antares-lucide-icons") keeps only these, so the
// binary carries a few hundred small chunks instead. A name outside this list does what the library
// already does for any name it cannot find: it shows the icon of the reply's `category`, or a dot.
// Every name must be a key of lucide's own map; the build fails on one that is not.
export const ICON_NAMES: readonly string[] = [
  // the library's category fallbacks, and its last resort
  "accessibility", "user", "cat", "arrow-right", "box", "building", "chart-line", "message-square", "wifi",
  "mouse-pointer", "palette", "code", "smartphone", "smile", "file", "dollar-sign", "cooking-pot", "gamepad",
  "house", "layout", "mail", "calculator", "heart-pulse", "music", "tree-pine", "map-pin", "bell", "users",
  "camera", "microscope", "sun", "shield", "circle", "shopping-cart", "share-2", "trophy", "leaf", "type",
  "clock", "wrench", "car", "plane", "cloud", "circle-dot",
  // status and feedback
  "check", "check-check", "circle-check", "circle-x", "circle-alert", "circle-help", "circle-play", "circle-pause",
  "circle-stop", "triangle-alert", "octagon-alert", "info", "x", "ban", "badge-check", "shield-check", "shield-alert",
  "thumbs-up", "thumbs-down", "flag", "bug", "zap", "sparkles", "star", "heart", "flame", "award", "medal", "target",
  "lightbulb", "rocket", "gift", "party-popper", "crown", "gem",
  // arrows and navigation
  "arrow-up", "arrow-down", "arrow-left", "arrow-up-right", "arrow-down-right", "arrow-up-down", "arrow-left-right",
  "chevron-up", "chevron-down", "chevron-left", "chevron-right", "chevrons-up-down", "corner-down-right", "move",
  "undo", "redo", "refresh-cw", "rotate-cw", "repeat", "shuffle", "external-link", "link", "compass", "map", "navigation",
  "globe", "locate", "route", "milestone",
  // actions
  "plus", "minus", "pencil", "trash-2", "copy", "clipboard", "clipboard-check", "save", "download", "upload", "share",
  "send", "search", "filter", "settings", "sliders-horizontal", "eye", "eye-off", "lock", "lock-open", "key", "log-in",
  "log-out", "power", "play", "pause", "square", "skip-forward", "skip-back", "fast-forward", "rewind", "menu", "ellipsis",
  "list", "list-checks", "list-todo", "list-ordered", "grip-vertical", "maximize", "minimize", "pin", "bookmark", "tag",
  "tags", "hash", "at-sign", "paperclip", "scissors", "wand-sparkles", "qr-code", "scan",
  // content and files
  "file-text", "file-code", "file-image", "file-spreadsheet", "file-check", "file-plus", "file-search", "folder",
  "folder-open", "folder-plus", "archive", "book", "book-open", "notebook", "newspaper", "library", "graduation-cap",
  "image", "images", "video", "film", "mic", "headphones", "volume-2", "podcast", "radio", "tv", "monitor",
  "quote", "text", "heading", "bold", "italic", "table", "table-2", "columns-2", "rows-2", "layout-dashboard",
  "layout-grid", "layout-list", "kanban", "panel-left", "square-code", "square-terminal", "terminal", "braces",
  "git-branch", "git-commit-horizontal", "git-merge", "git-pull-request", "github", "database", "server", "cpu",
  "hard-drive", "network", "cloud-upload", "cloud-download", "binary", "regex", "puzzle", "package", "blocks",
  // charts and numbers
  "chart-bar", "chart-pie", "chart-column", "chart-area", "chart-scatter", "chart-no-axes-column", "trending-up",
  "trending-down", "activity", "gauge", "percent", "scale", "sigma", "pi", "ruler", "timer", "hourglass",
  // time and calendar
  "calendar", "calendar-days", "calendar-check", "calendar-clock", "alarm-clock", "history", "watch",
  // money and commerce
  "wallet", "credit-card", "banknote", "coins", "receipt", "piggy-bank", "store", "shopping-bag", "package-check",
  "truck", "handshake", "briefcase", "building-2", "factory", "landmark", "hotel", "school", "hospital",
  // people and communication
  "user-round", "user-check", "user-plus", "circle-user", "users-round", "contact", "baby", "message-circle",
  "messages-square", "mail-open", "phone", "megaphone", "bell-ring", "rss", "languages", "brain", "bot",
  // places, travel and weather
  "tent", "mountain", "bed", "luggage", "ship", "train-front", "bus", "bike", "rocket", "fuel", "parking-meter",
  "umbrella", "snowflake", "wind", "droplets", "moon", "thermometer", "sunrise", "rainbow", "waves", "earth",
  // food, health, nature, home
  "utensils", "coffee", "wine", "beer", "pizza", "apple", "carrot", "egg", "cake", "ice-cream-cone", "salad",
  "stethoscope", "pill", "syringe", "dumbbell", "activity", "hand-heart", "flower", "sprout", "trees", "bird",
  "dog", "fish", "paw-print", "recycle", "zap-off", "lamp", "sofa", "bath", "refrigerator", "washing-machine",
  "shirt", "glasses", "hammer", "paintbrush", "pen-tool", "drama", "dices", "puzzle", "swords", "ghost",
  "flask-conical", "atom", "dna", "telescope", "satellite", "orbit", "battery", "plug", "bluetooth", "signal",
  "fingerprint", "shield-question-mark", "scale", "gavel", "vote", "badge-dollar-sign", "badge-info", "id-card",
];
