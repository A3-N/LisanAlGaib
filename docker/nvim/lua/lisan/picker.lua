local M = {}

local uv = vim.uv or vim.loop

local function current_directory()
  return uv.cwd() or uv.os_homedir() or vim.fn.expand "~"
end

local function filesystem_root(path)
  if vim.fn.has "win32" ~= 1 then
    return "/"
  end

  local absolute = vim.fn.fnamemodify(path, ":p")
  local drive = absolute:match "^([A-Za-z]:)[/\\]"
  if drive then
    return drive .. "\\"
  end

  -- Preserve the share root when Neovim was launched from a UNC path.
  local share = absolute:match "^([/\\][/\\][^/\\]+[/\\][^/\\]+)"
  return share or absolute
end

local function file_browser()
  require("lazy").load { plugins = { "telescope-file-browser.nvim" } }

  local telescope = require "telescope"
  if not telescope.extensions.file_browser then
    telescope.load_extension "file_browser"
  end

  return telescope.extensions.file_browser
end

local function picker_layout()
  return {
    layout_strategy = "horizontal",
    layout_config = {
      height = 0.85,
      prompt_position = "top",
      width = 0.9,
    },
    sorting_strategy = "ascending",
  }
end

local function open_workspace_tree(path)
  -- Load and open the same NvimTree instance used by NvChad's Ctrl-N mapping.
  -- API open is intentionally not a toggle: choosing a workspace must never
  -- close a tree that is already visible.
  require("lazy").load { plugins = { "nvim-tree.lua" } }
  require("nvim-tree.api").tree.open { path = path }
end

local function browse_files(root, prompt_title, results_title)
  local opts = picker_layout()

  opts.cwd = root
  opts.path = root
  opts.cwd_to_path = true
  opts.files = true
  opts.add_dirs = true
  opts.depth = 1
  opts.grouped = true
  opts.hidden = false
  opts.hide_parent_dir = true
  opts.create_from_prompt = false
  opts.prompt_title = prompt_title
  opts.results_title = results_title

  file_browser().file_browser(opts)
end

local function enable_dashboard_mouse()
  local buf = vim.g.nvdash_buf
  if not buf or not vim.api.nvim_buf_is_valid(buf) or vim.b[buf].lisan_dashboard_mouse then
    return
  end

  vim.b[buf].lisan_dashboard_mouse = true
  vim.keymap.set("n", "<LeftMouse>", function()
    local position = vim.fn.getmousepos()
    if position.winid == 0 or vim.api.nvim_win_get_buf(position.winid) ~= buf then
      return
    end

    vim.api.nvim_set_current_win(position.winid)
    vim.api.nvim_win_set_cursor(position.winid, { position.line, math.max(position.column - 1, 0) })
    vim.schedule(function()
      local enter = vim.api.nvim_replace_termcodes("<CR>", true, false, true)
      vim.api.nvim_feedkeys(enter, "m", false)
    end)
  end, { buffer = buf, desc = "Activate NvChad dashboard item" })
end

function M.workspace_label()
  enable_dashboard_mouse()
  return "  Choose Workspace  " .. vim.fn.fnamemodify(current_directory(), ":~") .. "  "
end

function M.set_workspace(path)
  local real_path = uv.fs_realpath(path) or path
  local stat = uv.fs_stat(real_path)

  if not stat or stat.type ~= "directory" then
    vim.notify("Choose a directory for the workspace", vim.log.levels.WARN, { title = "Lisan" })
    return false
  end

  vim.api.nvim_set_current_dir(real_path)
  open_workspace_tree(real_path)
  vim.notify("Workspace: " .. vim.fn.fnamemodify(real_path, ":~"), vim.log.levels.INFO, { title = "Lisan" })
  return true
end

function M.toggle_tree()
  local root = current_directory()
  require("lazy").load { plugins = { "nvim-tree.lua" } }
  require("nvim-tree.api").tree.toggle {
    path = root,
    update_root = true,
    focus = true,
  }
end

function M.choose_workspace()
  local actions = require "telescope.actions"
  local action_state = require "telescope.actions.state"
  local root = uv.os_homedir() or current_directory()
  local opts = picker_layout()

  opts.cwd = root
  opts.path = root
  opts.cwd_to_path = true
  opts.files = false
  opts.depth = 4
  opts.hidden = false
  opts.git_status = false
  opts.previewer = false
  opts.create_from_prompt = false
  opts.prompt_title = "Choose Workspace  •  Enter or double-click"
  opts.results_title = "Folders below " .. vim.fn.fnamemodify(root, ":~")
  opts.attach_mappings = function(prompt_bufnr)
    actions.select_default:replace(function()
      local entry = action_state.get_selected_entry()
      if not entry or not entry.Path then
        return
      end

      local path = entry.Path:absolute()
      actions.close(prompt_bufnr)
      vim.schedule(function()
        M.set_workspace(path)
      end)
    end)
    return true
  end

  file_browser().file_browser(opts)
end

function M.open_file()
  local root = current_directory()
  browse_files(root, "Open One File  •  Enter folders to browse", vim.fn.fnamemodify(root, ":~"))
end

function M.browse_filesystem()
  local root = filesystem_root(current_directory())
  browse_files(root, "Browse Filesystem  •  Enter folders to browse", root)
end

return M
