<?php
/** @var \NimVor\View\View $this */
echo $this->partial('dashboard/tile', ['key' => 'mem', 'label' => 'Memory', 'icon' => 'memory', 'series' => 'mem']);
