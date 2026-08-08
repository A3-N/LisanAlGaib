return {
  {
    "nvim-telescope/telescope-file-browser.nvim",
    dependencies = {
      "nvim-telescope/telescope.nvim",
      "nvim-lua/plenary.nvim",
    },
    init = function()
      local function map_current_tree()
        -- NvChad installs its default Ctrl-N mapping after lazy setup. Apply
        -- Lisan's current-directory-aware version after startup so it wins.
        vim.keymap.set("n", "<C-n>", function()
          require("lisan.picker").toggle_tree()
        end, { desc = "Toggle file tree at current directory" })
      end

      if vim.v.vim_did_enter == 1 then
        map_current_tree()
      else
        vim.api.nvim_create_autocmd("VimEnter", { once = true, callback = map_current_tree })
      end
    end,
    config = function()
      require("telescope").load_extension "file_browser"
    end,
  },
}
