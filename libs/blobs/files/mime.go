package files

// mimeMap maps file extensions (lowercase, no dot) to MIME types.
// Mirrors jsx-packages/libs/blobs/src/common/utils/values.ts mimeMap exactly.
var mimeMap = map[string]string{
	// Text & Documents
	"txt":  "text/plain",
	"csv":  "text/csv",
	"html": "text/html",
	"htm":  "text/html",
	"css":  "text/css",
	"js":   "application/javascript",
	"json": "application/json",
	"xml":  "application/xml",
	"md":   "text/markdown",
	"yaml": "application/x-yaml",
	"yml":  "application/x-yaml",
	"pdf":  "application/pdf",
	"rtf":  "application/rtf",
	"doc":  "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls":  "application/vnd.ms-excel",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt":  "application/vnd.ms-powerpoint",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",

	// Images
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
	"svg":  "image/svg+xml",
	"webp": "image/webp",
	"bmp":  "image/bmp",
	"ico":  "image/x-icon",
	"tif":  "image/tiff",
	"tiff": "image/tiff",
	"avif": "image/avif",
	"heic": "image/heic",

	// Audio
	"mp3":  "audio/mpeg",
	"wav":  "audio/wav",
	"ogg":  "audio/ogg",
	"m4a":  "audio/mp4",
	"flac": "audio/flac",
	"aac":  "audio/aac",

	// Video
	"mp4":  "video/mp4",
	"webm": "video/webm",
	"mov":  "video/quicktime",
	"avi":  "video/x-msvideo",
	"mkv":  "video/x-matroska",
	"mpeg": "video/mpeg",

	// Archives & Packages
	"zip": "application/zip",
	"gz":  "application/gzip",
	"tar": "application/x-tar",
	"rar": "application/vnd.rar",
	"7z":  "application/x-7z-compressed",
	"bz2": "application/x-bzip2",
	"jar": "application/java-archive",

	// Fonts
	"ttf":   "font/ttf",
	"otf":   "font/otf",
	"woff":  "font/woff",
	"woff2": "font/woff2",

	// Code / Config
	"sh":   "application/x-sh",
	"py":   "text/x-python",
	"ts":   "application/typescript",
	"tsx":  "application/typescript",
	"jsx":  "text/jsx",
	"java": "text/x-java-source",
	"go":   "text/x-go",
	"rs":   "text/rust",
	"cpp":  "text/x-c++src",
	"c":    "text/x-c",
	"sql":  "application/sql",
	"env":  "text/plain",

	// Others
	"bin":  "application/octet-stream",
	"exe":  "application/x-msdownload",
	"wasm": "application/wasm",
	"dmg":  "application/x-apple-diskimage",
	"iso":  "application/x-iso9660-image",
}
